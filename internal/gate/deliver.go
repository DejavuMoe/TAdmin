package gate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tadmin/internal/captcha"
	"tadmin/internal/i18n"
	"tadmin/internal/store"
	"tadmin/internal/tg"
)

// view identifies one rendered challenge message. eph > 0 marks an
// ephemeral message visible only to the gate's user.
type view struct {
	chat int64
	msg  int
	eph  int
}

var errHandled = errors.New("handled")

func (e *Engine) challenge(g *store.Gate) (*captcha.Challenge, error) {
	kind, ok := captcha.ParseKind(g.Kind)
	if !ok {
		kind = captcha.KindImage
	}
	return captcha.Generate(kind, g.Seed, g.Gen, g.Lang)
}

func remaining(d time.Duration, lang string) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	m, s := int(d/time.Minute), int(d%time.Minute/time.Second)
	if lang == "en" {
		if m == 0 {
			return fmt.Sprintf("%ds", s)
		}
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	if m == 0 {
		return fmt.Sprintf("%d 秒", s)
	}
	return fmt.Sprintf("%d 分 %02d 秒", m, s)
}

func (e *Engine) chatTitle(ctx context.Context, chatID int64) string {
	if c, err := e.st.GetChat(ctx, chatID); err == nil && c.Title != "" {
		return c.Title
	}
	return strconv.FormatInt(chatID, 10)
}

// caption renders the challenge message text.
func (e *Engine) caption(ctx context.Context, g *store.Gate, ch *captcha.Challenge, note string) string {
	lang := g.Lang
	title := tg.Esc(e.chatTitle(ctx, g.ChatID))
	var b strings.Builder
	if g.Source == store.SourceJoin {
		b.WriteString(i18n.T(lang, "cap_head_join", title))
	} else {
		b.WriteString(i18n.T(lang, "cap_head_request", title))
	}
	b.WriteString("\n\n")
	b.WriteString(tg.MentionID(g.UserID, g.UserName))
	b.WriteString(i18n.T(lang, "cap_comma"))
	b.WriteString("<b>" + tg.Esc(ch.Prompt) + "</b>")
	b.WriteString("\n\n")
	b.WriteString(i18n.T(lang, "cap_footer", remaining(g.Deadline.Sub(e.now()), lang), g.MaxAttempts-g.Attempts, g.MaxAttempts))
	if note != "" {
		b.WriteString("\n\n" + note)
	}
	return b.String()
}

var pickMarks = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣"}

func parsePicks(s string) []int {
	if s == "" {
		return nil
	}
	var out []int
	for _, p := range strings.Split(s, ",") {
		if v, err := strconv.Atoi(p); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func formatPicks(p []int) string {
	parts := make([]string, len(p))
	for i, v := range p {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

// keyboard renders the answer keyboard. admin adds moderator buttons for
// messages visible to the whole group.
func (e *Engine) keyboard(g *store.Gate, ch *captcha.Challenge, admin bool, private bool) *tg.InlineKeyboard {
	lang := g.Lang
	id := itoa(g.ID)
	gen := strconv.Itoa(g.Gen)
	var rows [][]tg.Button
	switch ch.Kind {
	case captcha.KindImage:
		picks := parsePicks(g.Picks)
		for r := 0; r < captcha.GridSize; r++ {
			var row []tg.Button
			for c := 0; c < captcha.GridSize; c++ {
				cell := r*captcha.GridSize + c
				label := "·"
				for i, p := range picks {
					if p == cell && i < len(pickMarks) {
						label = pickMarks[i]
					}
				}
				row = append(row, tg.CB(label, "g:"+id+":p:"+gen+":"+strconv.Itoa(cell)))
			}
			rows = append(rows, row)
		}
		rows = append(rows, tg.Row(tg.CB(i18n.T(lang, "btn_reset"), "g:"+id+":r:"+gen), tg.CB(i18n.T(lang, "btn_help"), "g:"+id+":h")))
	default:
		var row []tg.Button
		for i, opt := range ch.Options {
			b := tg.CB(opt, "g:"+id+":o:"+gen+":"+strconv.Itoa(i))
			if ch.Kind == captcha.KindButton {
				b.Style = tg.StyleSuccess
			}
			row = append(row, b)
			if len(row) == 3 {
				rows = append(rows, row)
				row = nil
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
		if ch.Kind != captcha.KindButton {
			rows = append(rows, tg.Row(tg.CB(i18n.T(lang, "btn_help"), "g:"+id+":h")))
		}
	}
	if private && e.webAppFor(g) {
		rows = append(rows, tg.Row(tg.Button{Text: i18n.T(lang, "btn_webapp"), WebApp: &tg.WebAppInfo{URL: e.WebAppURL(g)}}))
	}
	if admin {
		rows = append(rows, e.adminRow(g))
	}
	return &tg.InlineKeyboard{InlineKeyboard: rows}
}

func (e *Engine) adminRow(g *store.Gate) []tg.Button {
	id := itoa(g.ID)
	lang := g.Lang
	return tg.Row(
		tg.CBStyle(i18n.T(lang, "btn_approve"), "g:"+id+":a", tg.StyleSuccess),
		tg.CBStyle(i18n.T(lang, "btn_reject"), "g:"+id+":k", tg.StyleDanger),
	)
}

// sendChallenge sends a fresh challenge message.
func (e *Engine) sendChallenge(ctx context.Context, g *store.Gate, chatID int64, eph *tg.EphemeralParams, admin, private bool) (*tg.Message, error) {
	ch, err := e.challenge(g)
	if err != nil {
		return nil, err
	}
	caption := e.caption(ctx, g, ch, "")
	kb := e.keyboard(g, ch, admin, private)
	if ch.Image == nil {
		return e.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: chatID, Text: caption, ReplyMarkup: kb, Ephemeral: eph, DisableNotif: chatID < 0})
	}
	return e.tg.SendPhoto(ctx, tg.SendPhotoParams{ChatID: chatID, Caption: caption, ReplyMarkup: kb, Ephemeral: eph, DisableNotif: chatID < 0}, ch.Image)
}

// render updates an existing view after picks or a new generation.
func (e *Engine) render(ctx context.Context, g *store.Gate, v view, newImage bool, note string) error {
	ch, err := e.challenge(g)
	if err != nil {
		return err
	}
	private := v.chat > 0
	admin := v.eph == 0 && v.chat < 0
	kb := e.keyboard(g, ch, admin, private)
	caption := e.caption(ctx, g, ch, note)
	switch {
	case v.eph > 0 && newImage && ch.Image != nil:
		return e.tg.EditEphemeralPhoto(ctx, v.chat, g.UserID, v.eph, ch.Image, caption, kb)
	case v.eph > 0 && newImage:
		return e.tg.EditEphemeralText(ctx, v.chat, g.UserID, v.eph, caption, kb)
	case v.eph > 0:
		return e.tg.EditEphemeralReplyMarkup(ctx, v.chat, g.UserID, v.eph, kb)
	case newImage && ch.Image != nil:
		return e.tg.EditMessagePhoto(ctx, v.chat, v.msg, ch.Image, caption, kb)
	case newImage:
		return e.tg.EditMessageText(ctx, tg.EditMessageTextParams{ChatID: v.chat, MessageID: v.msg, Text: caption, ReplyMarkup: kb})
	default:
		return e.tg.EditMessageReplyMarkup(ctx, v.chat, v.msg, kb)
	}
}

// promptText renders the short public prompt for overlay/private delivery.
func (e *Engine) promptText(ctx context.Context, g *store.Gate) string {
	d := g.Deadline.Sub(e.now()).Round(time.Second)
	key := "prompt_overlay"
	if g.Delivery == store.DeliveryPrivate {
		key = "prompt_private"
	}
	return i18n.T(g.Lang, key, tg.MentionID(g.UserID, g.UserName), tg.Esc(e.chatTitle(ctx, g.ChatID)), remaining(d, g.Lang))
}

func (e *Engine) promptKeyboard(g *store.Gate) *tg.InlineKeyboard {
	var first tg.Button
	if g.Delivery == store.DeliveryPrivate {
		first = tg.Button{Text: i18n.T(g.Lang, "btn_go_private"), URL: e.DeepLink(g), Style: tg.StylePrimary}
	} else {
		first = tg.CBStyle(i18n.T(g.Lang, "btn_start"), "g:"+itoa(g.ID)+":s", tg.StylePrimary)
	}
	return tg.Keyboard(tg.Row(first), e.adminRow(g))
}

// deliver presents the challenge according to source and delivery mode and
// moves the gate to the challenge state.
func (e *Engine) deliver(ctx context.Context, chat *store.Chat, g *store.Gate) error {
	lockdown := g.Has(store.FlagLockdown)
	switch g.Source {
	case store.SourceGuard:
		// The Mini App was already shown when the query arrived.
	case store.SourceRequest:
		m, err := e.sendChallenge(ctx, g, g.UserChatID, nil, false, true)
		if err != nil {
			if tg.IsPermanent(err) {
				e.toReview(ctx, chat, g, "unreachable")
				return nil
			}
			return err
		}
		g.CaptchaChat, g.CaptchaMsg = m.Chat.ID, m.MessageID
		g.Flags |= store.FlagPrivate
	default:
		switch g.Delivery {
		case store.DeliveryPublic:
			m, err := e.sendChallenge(ctx, g, g.ChatID, nil, true, false)
			if err != nil {
				return err
			}
			g.CaptchaChat, g.CaptchaMsg = g.ChatID, m.MessageID
		case store.DeliveryPrivate:
			if lockdown {
				e.raidNotice(ctx, chat, chat.Lang(e.cfg.Lang))
			} else {
				m, err := e.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: g.ChatID, Text: e.promptText(ctx, g), ReplyMarkup: e.promptKeyboard(g), DisableNotif: true})
				if err != nil {
					return err
				}
				g.PromptMsg = m.MessageID
			}
		default: // overlay
			var promptErr error
			if lockdown {
				e.raidNotice(ctx, chat, chat.Lang(e.cfg.Lang))
			} else {
				m, err := e.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: g.ChatID, Text: e.promptText(ctx, g), ReplyMarkup: e.promptKeyboard(g), DisableNotif: true})
				if err == nil {
					g.PromptMsg = m.MessageID
				}
				promptErr = err
			}
			m, err := e.sendChallenge(ctx, g, g.ChatID, &tg.EphemeralParams{ReceiverUserID: g.UserID}, false, false)
			if err == nil && m.EphemeralMessageID > 0 {
				g.EphMsg = m.EphemeralMessageID
				g.Flags |= store.FlagEphemeral
				e.scheduleEphemeralCleanup(ctx, g, m.EphemeralMessageID)
			} else if promptErr != nil && !lockdown {
				// Neither path reached the user: fall back to a public challenge.
				m, err := e.sendChallenge(ctx, g, g.ChatID, nil, true, false)
				if err != nil {
					return err
				}
				g.CaptchaChat, g.CaptchaMsg = g.ChatID, m.MessageID
			}
		}
	}
	g.State, g.NextAt, g.Retries, g.LastError = store.StateChallenge, g.Deadline, 0, ""
	return e.st.SaveGate(ctx, g)
}

// scheduleEphemeralCleanup is a safety net deleting an ephemeral view after
// the verification window even if the explicit cleanup never runs.
func (e *Engine) scheduleEphemeralCleanup(ctx context.Context, g *store.Gate, id int) {
	_ = e.st.ScheduleDeletion(ctx, store.Deletion{ChatID: g.ChatID, MessageID: id, UserID: g.UserID, Due: g.Deadline.Add(2 * time.Minute)})
}

// showReview presents the manual review card.
func (e *Engine) showReview(ctx context.Context, chat *store.Chat, g *store.Gate, reason string) {
	text := e.reviewText(ctx, chat, g, reason)
	kb := tg.Keyboard(e.adminRow(g), tg.Row(tg.CBStyle(i18n.T(g.Lang, "btn_ban"), "g:"+itoa(g.ID)+":b", tg.StyleDanger)))
	if g.Source == store.SourceJoin {
		// Group admins handle join reviews in the group itself.
		if g.PromptMsg != 0 {
			if err := e.tg.EditMessageText(ctx, tg.EditMessageTextParams{ChatID: g.ChatID, MessageID: g.PromptMsg, Text: text, ReplyMarkup: kb}); err == nil {
				g.ReviewChat, g.ReviewMsg = g.ChatID, g.PromptMsg
				_ = e.st.SaveGate(ctx, g)
				e.updateUserViews(ctx, g, i18n.T(g.Lang, "user_review"))
				return
			}
		}
		if m, err := e.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: g.ChatID, Text: text, ReplyMarkup: kb, DisableNotif: true}); err == nil {
			g.ReviewChat, g.ReviewMsg = g.ChatID, m.MessageID
			_ = e.st.SaveGate(ctx, g)
			e.updateUserViews(ctx, g, i18n.T(g.Lang, "user_review"))
			return
		}
	}
	if m, err := e.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: e.cfg.OwnerID, Text: text, ReplyMarkup: kb}); err == nil {
		g.ReviewChat, g.ReviewMsg = e.cfg.OwnerID, m.MessageID
		_ = e.st.SaveGate(ctx, g)
	} else {
		e.log.Warn("review card undeliverable", "gate", g.ID, "error", err)
	}
	e.updateUserViews(ctx, g, i18n.T(g.Lang, "user_review"))
}

func (e *Engine) reviewText(ctx context.Context, chat *store.Chat, g *store.Gate, reason string) string {
	lang := g.Lang
	if g.ReviewChat == e.cfg.OwnerID || g.Source != store.SourceJoin {
		lang = e.cfg.Lang
	}
	var b strings.Builder
	b.WriteString(i18n.T(lang, "review_head", tg.Esc(chat.Title)))
	b.WriteString("\n\n")
	user := tg.MentionID(g.UserID, g.UserName)
	if g.UserUsername != "" {
		user += " @" + tg.Esc(g.UserUsername)
	}
	b.WriteString(i18n.T(lang, "review_user", user, g.UserID))
	b.WriteString("\n" + i18n.T(lang, "review_source", i18n.T(lang, "source_"+g.Source)))
	b.WriteString("\n" + i18n.T(lang, "review_reason", i18n.T(lang, "reason_"+reason)))
	b.WriteString("\n" + i18n.T(lang, "review_attempts", g.Attempts, g.MaxAttempts))
	if g.Risk >= 0 {
		b.WriteString("\n" + i18n.T(lang, "review_risk", int(g.Risk*100+0.5), tg.Esc(g.RiskReason)))
	}
	b.WriteString("\n" + i18n.T(lang, "review_deadline", remaining(g.NextAt.Sub(e.now()), lang)))
	return b.String()
}

// updateUserViews replaces private/ephemeral challenge views with a note.
func (e *Engine) updateUserViews(ctx context.Context, g *store.Gate, text string) {
	if g.EphMsg > 0 {
		// Challenge views are usually photos; captions need their own method.
		if err := e.tg.EditEphemeralCaption(ctx, g.ChatID, g.UserID, g.EphMsg, text, nil); err != nil {
			_ = e.tg.EditEphemeralText(ctx, g.ChatID, g.UserID, g.EphMsg, text, nil)
		}
	}
	if g.CaptchaChat > 0 && g.CaptchaMsg > 0 {
		if err := e.tg.EditMessageCaption(ctx, g.CaptchaChat, g.CaptchaMsg, text, nil); err != nil {
			_ = e.tg.EditMessageText(ctx, tg.EditMessageTextParams{ChatID: g.CaptchaChat, MessageID: g.CaptchaMsg, Text: text})
		}
	}
}

// cleanup removes or finalizes all verification messages.
func (e *Engine) cleanup(ctx context.Context, chat *store.Chat, g *store.Gate) {
	result := e.resultText(ctx, g)
	if g.EphMsg > 0 {
		e.dropEphemeral(ctx, g, g.EphMsg)
	}
	if g.CaptchaMsg > 0 {
		if g.CaptchaChat < 0 {
			_ = e.tg.DeleteMessage(ctx, g.CaptchaChat, g.CaptchaMsg)
		} else if note := e.userResultText(ctx, g); note != "" {
			e.updateUserViews(ctx, &store.Gate{CaptchaChat: g.CaptchaChat, CaptchaMsg: g.CaptchaMsg}, note)
		}
	}
	if g.ReviewMsg > 0 && result != "" {
		// Keep an audit trail on the review card, then tidy group copies.
		_ = e.tg.EditMessageText(ctx, tg.EditMessageTextParams{ChatID: g.ReviewChat, MessageID: g.ReviewMsg, Text: result})
		if g.ReviewChat < 0 {
			_ = e.st.ScheduleDeletion(ctx, store.Deletion{ChatID: g.ReviewChat, MessageID: g.ReviewMsg, Due: e.now().Add(time.Minute)})
		}
	}
	if g.PromptMsg > 0 && !(g.ReviewChat == g.ChatID && g.ReviewMsg == g.PromptMsg) {
		_ = e.tg.DeleteMessage(ctx, g.ChatID, g.PromptMsg)
	}
	if g.JoinMsg > 0 && chat.Settings.Clean.Join {
		_ = e.tg.DeleteMessage(ctx, g.ChatID, g.JoinMsg)
	}
}

func (e *Engine) actorName(ctx context.Context, g *store.Gate) string {
	if g.ActorID == 0 {
		return ""
	}
	if g.ActorID == e.cfg.OwnerID {
		return i18n.T(g.Lang, "actor_owner")
	}
	if m, err := e.tg.GetChatMember(ctx, g.ChatID, g.ActorID); err == nil {
		return tg.Mention(&m.User)
	}
	return tg.MentionID(g.ActorID, "")
}

// resultText is the admin-facing outcome shown on review cards.
func (e *Engine) resultText(ctx context.Context, g *store.Gate) string {
	lang := g.Lang
	if g.ReviewChat == e.cfg.OwnerID {
		lang = e.cfg.Lang
	}
	who := tg.MentionID(g.UserID, g.UserName)
	actor := e.actorName(ctx, g)
	key := "result_" + g.Outcome
	if actor != "" {
		return i18n.T(lang, key, who) + "\n" + i18n.T(lang, "result_by", actor)
	}
	return i18n.T(lang, key, who)
}

// userResultText is the note shown in the user's private challenge message.
func (e *Engine) userResultText(ctx context.Context, g *store.Gate) string {
	title := tg.Esc(e.chatTitle(ctx, g.ChatID))
	switch {
	case store.Positive(g.Outcome):
		if g.Source == store.SourceJoin {
			return i18n.T(g.Lang, "user_pass_join", title)
		}
		return i18n.T(g.Lang, "user_pass_request", title)
	case g.Outcome == store.OutcomeFail, g.Outcome == store.OutcomeTimeout, g.Outcome == store.OutcomeReject:
		return i18n.T(g.Lang, "user_fail", title)
	case g.Outcome == store.OutcomeLeft || g.Outcome == store.OutcomeCancel:
		return i18n.T(g.Lang, "user_closed")
	default:
		return i18n.T(g.Lang, "user_fail", title)
	}
}

// welcome sends the configured welcome message.
func (e *Engine) welcome(ctx context.Context, chat *store.Chat, g *store.Gate) {
	w := chat.Settings.Welcome
	if !w.Enabled {
		return
	}
	lang := chat.Lang(e.cfg.Lang)
	text, kb := RenderWelcome(w, lang, g.UserID, g.UserName, g.UserUsername, chat.Title)
	p := tg.SendMessageParams{ChatID: chat.ID, Text: text, ReplyMarkup: kb, DisableNotif: true}
	if w.Ephemeral {
		p.Ephemeral = &tg.EphemeralParams{ReceiverUserID: g.UserID}
	}
	m, err := e.tg.SendMessage(ctx, p)
	if err != nil {
		e.log.Warn("welcome failed", "chat", chat.ID, "error", err)
		return
	}
	if w.DeleteAfter > 0 {
		d := store.Deletion{ChatID: chat.ID, MessageID: m.MessageID, Due: e.now().Add(time.Duration(w.DeleteAfter) * time.Second)}
		if m.EphemeralMessageID > 0 {
			d.MessageID, d.UserID = m.EphemeralMessageID, g.UserID
		}
		_ = e.st.ScheduleDeletion(ctx, d)
	}
}

// RenderWelcome renders a welcome template and its button lines.
// Placeholders: {name} {mention} {username} {group}.
func RenderWelcome(w store.Welcome, lang string, userID int64, name, username, group string) (string, *tg.InlineKeyboard) {
	tpl := w.Text
	if strings.TrimSpace(tpl) == "" {
		tpl = i18n.T(lang, "welcome_default")
	}
	uname := ""
	if username != "" {
		uname = "@" + username
	}
	text := strings.NewReplacer(
		"{name}", tg.Esc(name), "{mention}", tg.MentionID(userID, name),
		"{username}", tg.Esc(uname), "{group}", tg.Esc(group),
	).Replace(tpl)
	return text, ParseButtons(w.Buttons)
}

// ParseButtons parses lines of "Label - https://url" (several per line
// separated by "|") into a keyboard.
func ParseButtons(spec string) *tg.InlineKeyboard {
	var rows [][]tg.Button
	for _, line := range strings.Split(spec, "\n") {
		var row []tg.Button
		for _, cell := range strings.Split(line, "|") {
			label, url, ok := strings.Cut(cell, " - ")
			label, url = strings.TrimSpace(label), strings.TrimSpace(url)
			if !ok || label == "" || !(strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "tg://")) {
				continue
			}
			row = append(row, tg.URLButton(label, url))
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return &tg.InlineKeyboard{InlineKeyboard: rows}
}

// webAppFor reports whether the Mini App button should be offered for g.
func (e *Engine) webAppFor(g *store.Gate) bool {
	if e.cfg.WebURL == "" {
		return false
	}
	c, err := e.st.GetChat(context.Background(), g.ChatID)
	return err != nil || c.Settings.Verify.WebApp
}

// dropEphemeral deletes an ephemeral view and its safety-net deletion:
// Telegram reuses ephemeral ids, so a stale schedule could hit a new message.
func (e *Engine) dropEphemeral(ctx context.Context, g *store.Gate, id int) {
	_ = e.tg.DeleteEphemeral(ctx, g.ChatID, g.UserID, id)
	_ = e.st.DoneDeletion(ctx, store.Deletion{ChatID: g.ChatID, MessageID: id, UserID: g.UserID})
}
