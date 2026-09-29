package gate

import (
	"context"
	"strings"
	"time"

	"tadmin/internal/captcha"
	"tadmin/internal/i18n"
	"tadmin/internal/store"
	"tadmin/internal/tg"
)

// isJoin reports whether a chat_member transition means the user entered.
func isJoin(u *tg.ChatMemberUpdated) bool {
	return !u.OldChatMember.IsPresent() && u.NewChatMember.IsPresent()
}

// isLeave reports whether a chat_member transition means the user left.
func isLeave(u *tg.ChatMemberUpdated) bool {
	return u.OldChatMember.IsPresent() && !u.NewChatMember.IsPresent()
}

// OnMemberUpdate handles chat_member updates of a managed chat.
func (e *Engine) OnMemberUpdate(ctx context.Context, chat *store.Chat, u *tg.ChatMemberUpdated) {
	user := u.NewChatMember.User
	switch {
	case isLeave(u):
		e.cfg.Roles.Invalidate(chat.ID)
		e.OnLeft(ctx, chat.ID, user.ID)
		return
	case !isJoin(u):
		if u.OldChatMember.IsAdmin() != u.NewChatMember.IsAdmin() {
			e.cfg.Roles.Invalidate(chat.ID)
			if u.NewChatMember.IsAdmin() {
				// Promoted while verifying: the admin is trusted now.
				e.cancelOpen(ctx, chat.ID, user.ID, u.From.ID)
			}
		}
		return
	}
	e.admit(ctx, chat, &user, &u.From, u.InviteLink, u.ViaJoinRequest)
}

// OnNewMembers handles the join service message. Telegram also sends a
// chat_member update carrying more detail (invite link, join request); the
// service message only acts as a fallback and never overrides exemptions
// decided from the richer update.
func (e *Engine) OnNewMembers(ctx context.Context, chat *store.Chat, msg *tg.Message) {
	if msg.From == nil {
		return
	}
	for i := range msg.NewChatMembers {
		user := msg.NewChatMembers[i]
		e.admitFrom(ctx, chat, &user, msg.From, nil, false, true)
	}
}

// admit decides whether a newcomer needs verification and starts it.
func (e *Engine) admit(ctx context.Context, chat *store.Chat, user, actor *tg.User, link *tg.ChatInviteLink, viaRequest bool) {
	e.admitFrom(ctx, chat, user, actor, link, viaRequest, false)
}

// exemption returns a non-empty reason when user skips verification.
func (e *Engine) exemption(ctx context.Context, chat *store.Chat, user, actor *tg.User, link *tg.ChatInviteLink, viaRequest bool) string {
	switch {
	case user.ID == e.cfg.OwnerID:
		return "owner"
	case viaRequest:
		// Join requests are verified before approval (by us or an admin).
		return "join_request"
	}
	if ok, _ := e.st.RecentPass(ctx, chat.ID, user.ID, e.now().Add(-10*time.Minute)); ok {
		return "request_passed"
	}
	if actor != nil && actor.ID != user.ID && actor.ID != 0 {
		if adm, err := e.cfg.Roles.IsAdmin(ctx, chat.ID, actor.ID); err == nil && adm {
			return "added_by_admin"
		}
	}
	if link != nil && isTrustedLink(link.Name) {
		return "trusted_link"
	}
	if adm, err := e.cfg.Roles.IsAdmin(ctx, chat.ID, user.ID); err == nil && adm {
		return "admin"
	}
	return ""
}

func (e *Engine) admitFrom(ctx context.Context, chat *store.Chat, user, actor *tg.User, link *tg.ChatInviteLink, viaRequest, fallback bool) {
	if user.ID == e.cfg.BotID || chat.Status != store.ChatActive {
		return
	}
	lang := chat.Lang(e.cfg.Lang)
	if user.IsBot {
		e.handleBotJoin(ctx, chat, user, actor)
		return
	}
	if !chat.Settings.Verify.Enabled {
		return
	}
	existing, _ := e.st.OpenGateFor(ctx, chat.ID, user.ID)
	if reason := e.exemption(ctx, chat, user, actor, link, viaRequest); reason != "" {
		if !fallback {
			if existing != nil && existing.Has(store.FlagFallback) && existing.Source == store.SourceJoin {
				// The service message arrived first and started a gate
				// without knowing about the exemption: undo it.
				e.cancelOpen(ctx, chat.ID, user.ID, 0)
			}
			if reason != "owner" && reason != "admin" {
				e.record(ctx, chat.ID, store.EvExempt, user.ID, actorID(actor), 0, reason)
			}
		}
		return
	}
	if fallback {
		// The chat_member update may have been handled already and decided
		// on an exemption this message cannot see.
		if ok, _ := e.st.HasRecentEvent(ctx, chat.ID, user.ID, store.EvExempt, e.now().Add(-2*time.Minute)); ok {
			return
		}
	}
	if existing != nil {
		if existing.Source == store.SourceJoin {
			return // duplicate notification
		}
		// A pending join request does not cover a direct join through
		// another link: retire it and verify the member now.
		e.cancelOpen(ctx, chat.ID, user.ID, 0)
	}

	now := e.now()
	lockdown := e.checkRaid(ctx, chat, now)
	v := chat.Settings.Verify
	timeout := time.Duration(v.Timeout) * time.Second
	flags := 0
	if fallback {
		flags |= store.FlagFallback
	}
	delivery := v.Delivery
	if lockdown {
		flags |= store.FlagLockdown
		timeout = min(timeout, 90*time.Second)
		if delivery == store.DeliveryPublic {
			delivery = store.DeliveryOverlay
		}
	}
	kind := v.Kind
	if _, ok := captcha.ParseKind(kind); !ok {
		kind = string(captcha.KindImage)
	}
	g := &store.Gate{
		ChatID: chat.ID, UserID: user.ID, Source: store.SourceJoin, State: store.StateNew,
		Delivery: delivery, Kind: kind, Seed: newSeed(), Gen: 1, MaxAttempts: max(1, v.Attempts),
		Deadline: now.Add(timeout), NextAt: now, Token: newToken(),
		UserName: user.FullName(), UserUsername: user.Username, Lang: userLang(user, lang), Risk: -1, Flags: flags,
	}
	created, _, err := e.st.CreateGate(ctx, g)
	if err != nil {
		e.log.Error("create gate", "chat", chat.ID, "user", user.ID, "error", err)
		return
	}
	if !created {
		return
	}
	e.record(ctx, chat.ID, store.EvJoin, user.ID, 0, 0, "")
	e.log.Info("verification started", "chat", chat.ID, "user", user.ID, "gate", g.ID, "delivery", delivery, "kind", kind)
	// Mute right away in this goroutine: it is the latency-critical action.
	// Checks and challenge delivery run in the scheduler so a burst of joins
	// never holds up the update loop.
	e.restrictNow(ctx, g.ID)
	e.Wake()
}

func actorID(u *tg.User) int64 {
	if u == nil {
		return 0
	}
	return u.ID
}
func isTrustedLink(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "#免验证") || strings.Contains(n, "#noverify") || strings.Contains(n, "#trusted")
}

func userLang(u *tg.User, fallback string) string {
	lc := strings.ToLower(u.LanguageCode)
	switch {
	case lc == "":
		return fallback
	case strings.HasPrefix(lc, "zh"):
		return "zh-CN"
	default:
		return "en"
	}
}

func (e *Engine) handleBotJoin(ctx context.Context, chat *store.Chat, bot, actor *tg.User) {
	if !chat.Settings.KickBots || actor == nil || actor.ID == e.cfg.OwnerID {
		return
	}
	// Both the chat_member update and the service message report the join.
	if !e.once("botjoin:"+itoa(chat.ID)+":"+itoa(bot.ID), time.Minute) {
		return
	}
	if adm, err := e.cfg.Roles.IsAdmin(ctx, chat.ID, actor.ID); err != nil || adm {
		return
	}
	if err := e.tg.BanChatMember(ctx, chat.ID, bot.ID, time.Time{}, false); err != nil {
		e.log.Warn("kick bot", "chat", chat.ID, "bot", bot.ID, "error", err)
		return
	}
	e.record(ctx, chat.ID, store.EvBotKicked, bot.ID, actor.ID, 0, bot.Username)
	lang := chat.Lang(e.cfg.Lang)
	m, err := e.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: chat.ID, DisableNotif: true,
		Text: i18n.T(lang, "bot_kicked", tg.Esc("@"+bot.Username), tg.Mention(actor))})
	if err == nil {
		_ = e.st.ScheduleDeletion(ctx, store.Deletion{ChatID: chat.ID, MessageID: m.MessageID, Due: e.now().Add(time.Minute)})
	}
}

// OnLeft closes an open gate when the user leaves.
func (e *Engine) OnLeft(ctx context.Context, chatID, userID int64) {
	g, err := e.st.OpenGateFor(ctx, chatID, userID)
	if err != nil {
		return
	}
	unlock := e.lock(g.ID)
	defer unlock()
	if g, err = e.st.GetGate(ctx, g.ID); err != nil || !g.Open() || g.State == store.StateResolving {
		return
	}
	if g.Source != store.SourceJoin {
		return
	}
	e.resolve(ctx, g, store.OutcomeLeft, 0)
}

func (e *Engine) cancelOpen(ctx context.Context, chatID, userID, actor int64) {
	g, err := e.st.OpenGateFor(ctx, chatID, userID)
	if err != nil {
		return
	}
	unlock := e.lock(g.ID)
	defer unlock()
	if g, err = e.st.GetGate(ctx, g.ID); err != nil || !g.Open() || g.State == store.StateResolving {
		return
	}
	e.resolve(ctx, g, store.OutcomeCancel, actor)
}

// OnJoinRequest handles chat_join_request updates.
func (e *Engine) OnJoinRequest(ctx context.Context, chat *store.Chat, r *tg.ChatJoinRequest) {
	if chat.Status != store.ChatActive || !chat.Settings.Verify.Enabled || !chat.Settings.Verify.Requests {
		if r.QueryID != "" {
			_ = e.tg.AnswerJoinRequestQuery(ctx, r.QueryID, tg.JoinQueue)
		}
		return
	}
	lang := chat.Lang(e.cfg.Lang)
	now := e.now()
	v := chat.Settings.Verify
	timeout := time.Duration(v.Timeout) * time.Second
	source := store.SourceRequest
	if r.QueryID != "" && e.cfg.WebURL != "" {
		source = store.SourceGuard
	}
	if source == store.SourceRequest && timeout > 4*time.Minute+30*time.Second {
		// user_chat_id may only be used for 5 minutes.
		timeout = 4*time.Minute + 30*time.Second
	}
	kind := v.Kind
	if _, ok := captcha.ParseKind(kind); !ok || (source == store.SourceGuard && kind == string(captcha.KindButton)) {
		kind = string(captcha.KindImage)
	}
	g := &store.Gate{
		ChatID: chat.ID, UserID: r.From.ID, Source: source, State: store.StateNew, Delivery: store.DeliveryPrivate,
		Kind: kind, Seed: newSeed(), Gen: 1, MaxAttempts: max(1, v.Attempts), Deadline: now.Add(timeout), NextAt: now.Add(time.Minute),
		Token: newToken(), UserName: r.From.FullName(), UserUsername: r.From.Username, Lang: userLang(&r.From, lang),
		UserChatID: r.UserChatID, QueryID: r.QueryID, Risk: -1,
	}
	created, existing, err := e.st.CreateGate(ctx, g)
	if err != nil {
		e.log.Error("create request gate", "chat", chat.ID, "user", r.From.ID, "error", err)
		return
	}
	if !created {
		e.repeatRequest(ctx, existing.ID, r)
		return
	}
	e.record(ctx, chat.ID, store.EvJoin, r.From.ID, 0, 0, source)
	// The gate was created with a future NextAt so the scheduler leaves it
	// alone until the Guard query has been answered (10-second limit).
	unlock := e.lock(g.ID)
	if source == store.SourceGuard {
		if err := e.tg.SendJoinRequestWebApp(ctx, r.QueryID, e.WebAppURL(g)); err != nil {
			e.log.Warn("guard web app failed; queueing for regular verification", "chat", chat.ID, "error", err)
			_ = e.tg.AnswerJoinRequestQuery(ctx, r.QueryID, tg.JoinQueue)
			g.Source, g.QueryID = store.SourceRequest, ""
			// user_chat_id may only be used for 5 minutes.
			g.Deadline = minTime(g.Deadline, now.Add(4*time.Minute+30*time.Second))
		}
	} else if r.QueryID != "" {
		_ = e.tg.AnswerJoinRequestQuery(ctx, r.QueryID, tg.JoinQueue)
	}
	g.NextAt = e.now()
	_ = e.st.SaveGate(ctx, g)
	unlock()
	e.Wake()
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// repeatRequest handles a join request for a user who already has an open
// gate: refresh the contact details and answer a new Guard query.
func (e *Engine) repeatRequest(ctx context.Context, id int64, r *tg.ChatJoinRequest) {
	unlock := e.lock(id)
	defer unlock()
	g, err := e.st.GetGate(ctx, id)
	if err != nil || !g.Answering() || g.Source == store.SourceJoin {
		if r.QueryID != "" {
			_ = e.tg.AnswerJoinRequestQuery(ctx, r.QueryID, tg.JoinQueue)
		}
		return
	}
	g.UserChatID = r.UserChatID
	if r.QueryID != "" && r.QueryID != g.QueryID {
		if g.Source == store.SourceGuard && e.tg.SendJoinRequestWebApp(ctx, r.QueryID, e.WebAppURL(g)) == nil {
			g.QueryID = r.QueryID
		} else {
			_ = e.tg.AnswerJoinRequestQuery(ctx, r.QueryID, tg.JoinQueue)
		}
	}
	_ = e.st.SaveGate(ctx, g)
}

// checkRaid counts recent joins and enters lockdown when the threshold is hit.
func (e *Engine) checkRaid(ctx context.Context, chat *store.Chat, now time.Time) bool {
	if chat.InLockdown(now) {
		return true
	}
	r := chat.Settings.Raid
	if r.Threshold <= 0 {
		return false
	}
	n, err := e.st.CountEvents(ctx, chat.ID, store.EvJoin, now.Add(-time.Minute))
	if err != nil || n+1 < r.Threshold {
		return false
	}
	until := now.Add(time.Duration(max(1, r.Minutes)) * time.Minute)
	if err := e.st.SetLockdown(ctx, chat.ID, until); err != nil {
		return false
	}
	chat.LockdownUntil = until
	e.record(ctx, chat.ID, store.EvRaid, 0, 0, int64(n+1), "")
	e.log.Warn("raid lockdown", "chat", chat.ID, "joins_per_minute", n+1)
	lang := chat.Lang(e.cfg.Lang)
	e.alert(ctx, "raid:"+itoa(chat.ID), i18n.T(e.cfg.Lang, "raid_owner", tg.Esc(chat.Title), n+1, r.Minutes),
		tg.Keyboard(tg.Row(tg.CBStyle(i18n.T(e.cfg.Lang, "raid_lift"), "rl:"+itoa(chat.ID), tg.StyleSuccess))))
	e.raidNotice(ctx, chat, lang)
	return true
}

// raidNotice posts (at most once per minute) a shared verification button
// used during lockdown instead of per-user public prompts.
func (e *Engine) raidNotice(ctx context.Context, chat *store.Chat, lang string) {
	e.mu.Lock()
	if last, ok := e.notices[chat.ID]; ok && e.now().Sub(last) < time.Minute {
		e.mu.Unlock()
		return
	}
	e.notices[chat.ID] = e.now()
	e.mu.Unlock()
	m, err := e.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: chat.ID, DisableNotif: true,
		Text:        i18n.T(lang, "raid_notice"),
		ReplyMarkup: tg.Keyboard(tg.Row(tg.CBStyle(i18n.T(lang, "btn_start"), "gs:"+itoa(chat.ID), tg.StylePrimary)))})
	if err == nil {
		due := chat.LockdownUntil.Add(2 * time.Minute)
		if due.Before(e.now().Add(5 * time.Minute)) {
			due = e.now().Add(5 * time.Minute)
		}
		_ = e.st.ScheduleDeletion(ctx, store.Deletion{ChatID: chat.ID, MessageID: m.MessageID, Due: due})
	}
}

// LiftLockdown ends raid protection early.
func (e *Engine) LiftLockdown(ctx context.Context, chatID int64) error {
	return e.st.SetLockdown(ctx, chatID, time.Time{})
}

func (e *Engine) record(ctx context.Context, chatID int64, kind string, user, actor, value int64, data string) {
	if err := e.st.AddEvent(ctx, store.Event{ChatID: chatID, Kind: kind, UserID: user, ActorID: actor, Value: value, Data: data}); err != nil {
		e.log.Warn("record event", "kind", kind, "error", err)
	}
}
