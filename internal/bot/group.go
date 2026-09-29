package bot

import (
	"context"
	"strconv"
	"strings"
	"time"

	"tadmin/internal/store"
	"tadmin/internal/tg"
)

func isServiceOther(m *tg.Message) bool {
	return m.NewChatTitle != "" || m.NewChatPhoto != nil || m.DeleteChatPhoto || m.VideoChatStarted != nil ||
		m.VideoChatEnded != nil || m.VideoChatScheduled != nil || m.BoostAdded != nil || m.AutoDeleteChanged != nil ||
		m.ForumTopicCreated != nil
}

func (b *Bot) onGroupMessage(ctx context.Context, m *tg.Message) {
	if m.MigrateToChatID != 0 {
		if err := b.st.MigrateChat(ctx, m.Chat.ID, m.MigrateToChatID); err != nil {
			b.log.Error("migrate chat", "from", m.Chat.ID, "to", m.MigrateToChatID, "error", err)
		}
		return
	}
	chat := b.managed(ctx, m.Chat.ID)
	if chat == nil {
		// Allow the owner to (re)register a group the database forgot.
		if m.From != nil && b.isOwner(m.From.ID) && m.IsCommand() {
			if cmd, _, _ := tg.CommandArgs(m.Text); cmd == "settings" || cmd == "setup" {
				b.registerFromCommand(ctx, m)
			}
		}
		return
	}
	cl := chat.Settings.Clean
	del := func() { _ = b.tg.DeleteMessage(ctx, chat.ID, m.MessageID) }
	switch {
	case len(m.NewChatMembers) > 0:
		b.Gate.OnNewMembers(ctx, chat, m)
		if cl.Join {
			del()
		}
		return
	case m.LeftChatMember != nil:
		if cl.Leave {
			del()
		}
		return
	case m.PinnedMessage != nil:
		if cl.Pin {
			del()
		}
		return
	case isServiceOther(m):
		if cl.Other {
			del()
		}
		return
	}
	if m.IsAutomaticForward {
		b.Channel.OnAutoForward(ctx, chat, m)
		return
	}
	if m.SenderChat != nil && b.Channel.OnMask(ctx, chat, m) {
		return
	}
	if m.From != nil && !m.From.IsBot {
		// Anything a newcomer posts before the mute lands is removed; this
		// enforces the gate, it does not judge the content.
		if g, err := b.st.OpenGateFor(ctx, chat.ID, m.From.ID); err == nil && g.Source == store.SourceJoin {
			del()
			return
		}
	}
	if m.IsCommand() {
		b.onGroupCommand(ctx, chat, m)
	}
}

func (b *Bot) registerFromCommand(ctx context.Context, m *tg.Message) {
	c := &store.Chat{ID: m.Chat.ID, Title: m.Chat.Title, Username: m.Chat.Username, Status: store.ChatActive, AddedBy: m.From.ID}
	c.Settings.Language = b.lang
	if err := b.st.UpsertChat(ctx, c); err != nil {
		return
	}
	_ = b.st.SetChatStatus(ctx, c.ID, store.ChatActive)
	rep := b.refreshChat(ctx, c.ID)
	b.sendSetupCard(ctx, c.ID, c.Title, rep)
	_ = b.tg.DeleteMessage(ctx, m.Chat.ID, m.MessageID)
}

// reply sends a short group reply, deleted after a while when configured.
func (b *Bot) groupReply(ctx context.Context, chat *store.Chat, m *tg.Message, text string) {
	p := tg.SendMessageParams{ChatID: chat.ID, Text: text, DisableNotif: true,
		ReplyParameters: &tg.ReplyParameters{MessageID: m.MessageID, AllowSendingWithoutReply: true}}
	if m.EphemeralMessageID != 0 && m.From != nil {
		// Reply ephemerally to an ephemeral command.
		p.ReplyParameters = &tg.ReplyParameters{EphemeralMessageID: m.EphemeralMessageID}
		p.Ephemeral = &tg.EphemeralParams{ReceiverUserID: m.From.ID}
	}
	sent, err := b.tg.SendMessage(ctx, p)
	if err != nil && p.Ephemeral != nil {
		p.Ephemeral, p.ReplyParameters = nil, nil
		sent, err = b.tg.SendMessage(ctx, p)
	}
	if err != nil {
		return
	}
	if chat.Settings.Clean.Commands {
		d := store.Deletion{ChatID: chat.ID, MessageID: sent.MessageID, Due: time.Now().Add(30 * time.Second)}
		if sent.EphemeralMessageID != 0 {
			d.MessageID, d.UserID = sent.EphemeralMessageID, m.From.ID
		}
		_ = b.st.ScheduleDeletion(ctx, d)
	}
}

func (b *Bot) onGroupCommand(ctx context.Context, chat *store.Chat, m *tg.Message) {
	cmd, target, args := tg.CommandArgs(m.Text)
	if target != "" && !strings.EqualFold(target, b.Me.Username) {
		return
	}
	known := map[string]bool{"settings": true, "status": true, "pass": true, "kick": true, "ban": true, "mute": true,
		"unmute": true, "unban": true, "unpin": true, "setup": true, "help": true}
	if !known[cmd] {
		return
	}
	lang := chat.Lang(b.lang)
	if m.SenderChat != nil && m.SenderChat.ID == chat.ID {
		b.groupReply(ctx, chat, m, t(lang, "anon_admin"))
		return
	}
	if m.From == nil || !b.canManage(ctx, chat.ID, m.From.ID) {
		return // silently ignore commands from non-admins
	}
	if chat.Settings.Clean.Commands && m.EphemeralMessageID == 0 {
		defer func() { _ = b.tg.DeleteMessage(ctx, chat.ID, m.MessageID) }()
	}
	actor := m.From
	var targetUser *tg.User
	if m.ReplyToMessage != nil && m.ReplyToMessage.From != nil && !m.ReplyToMessage.From.IsBot {
		targetUser = m.ReplyToMessage.From
	}
	// Allow "/unban 123" or "/pass 123" by numeric id.
	if targetUser == nil {
		if f := strings.Fields(args); len(f) > 0 {
			if id, err := strconv.ParseInt(f[0], 10, 64); err == nil && id > 0 {
				targetUser = &tg.User{ID: id, FirstName: f[0]}
				args = strings.TrimSpace(strings.TrimPrefix(args, f[0]))
			}
		}
	}
	needTarget := func() bool {
		if targetUser == nil {
			b.groupReply(ctx, chat, m, t(lang, "cmd_need_reply"))
			return false
		}
		if b.isOwner(targetUser.ID) {
			return false
		}
		if adm, _ := b.Roles.IsAdmin(ctx, chat.ID, targetUser.ID); adm {
			b.groupReply(ctx, chat, m, t(lang, "cmd_target_admin"))
			return false
		}
		return true
	}
	record := func(action string) {
		_ = b.st.AddEvent(ctx, store.Event{ChatID: chat.ID, Kind: store.EvAdminCmd, UserID: targetUser.ID, ActorID: actor.ID, Data: action})
	}
	who := func() string { return tg.Mention(targetUser) }
	switch cmd {
	case "settings", "setup":
		b.sendGroupPanelTo(ctx, chat, m)
	case "help":
		b.groupReply(ctx, chat, m, t(lang, "group_help"))
	case "status":
		b.groupReply(ctx, chat, m, b.groupStatusLine(ctx, chat, lang))
	case "unpin":
		if !b.canPin(ctx, chat.ID, actor.ID) {
			b.groupReply(ctx, chat, m, t(lang, "need_pin_right"))
			return
		}
		// Unpinning hundreds of posts takes a while; never hold the update batch.
		b.background(func(ctx context.Context) {
			res, err := b.Channel.CleanPins(ctx, chat)
			if err != nil {
				b.groupReply(ctx, chat, m, t(lang, "unpin_failed", tg.Esc(err.Error())))
				return
			}
			b.groupReply(ctx, chat, m, t(lang, "unpin_done", res.Unpinned))
		})
	case "pass":
		if targetUser == nil {
			b.groupReply(ctx, chat, m, t(lang, "cmd_need_reply"))
			return
		}
		if err := b.Gate.DecideUser(ctx, chat.ID, targetUser.ID, store.OutcomeApprove, actor.ID); err != nil {
			b.groupReply(ctx, chat, m, t(lang, "pass_none", who()))
			return
		}
		b.groupReply(ctx, chat, m, t(lang, "pass_done", who()))
	case "kick":
		if !needTarget() {
			return
		}
		if err := b.tg.BanChatMember(ctx, chat.ID, targetUser.ID, time.Now().Add(2*time.Minute), false); err != nil {
			b.groupReply(ctx, chat, m, t(lang, "cmd_failed", tg.Esc(err.Error())))
			return
		}
		record("kick")
		b.groupReply(ctx, chat, m, t(lang, "kick_done", who()))
	case "ban":
		if !needTarget() {
			return
		}
		var until time.Time
		if f := firstField(args); f != "" {
			d, ok := parseDuration(f)
			if !ok || !validSpan(d) {
				b.groupReply(ctx, chat, m, t(lang, "dur_invalid"))
				return
			}
			until = time.Now().Add(d)
		}
		if err := b.tg.BanChatMember(ctx, chat.ID, targetUser.ID, until, until.IsZero()); err != nil {
			b.groupReply(ctx, chat, m, t(lang, "cmd_failed", tg.Esc(err.Error())))
			return
		}
		record("ban")
		if until.IsZero() {
			b.groupReply(ctx, chat, m, t(lang, "ban_done", who()))
		} else {
			b.groupReply(ctx, chat, m, t(lang, "ban_done_for", who(), tg.Esc(firstField(args))))
		}
	case "mute":
		if !needTarget() {
			return
		}
		d := time.Hour
		if f := firstField(args); f != "" {
			v, ok := parseDuration(f)
			if !ok || !validSpan(v) {
				b.groupReply(ctx, chat, m, t(lang, "dur_invalid"))
				return
			}
			d = v
		}
		if err := b.tg.RestrictChatMember(ctx, chat.ID, targetUser.ID, tg.Muted(), time.Now().Add(d)); err != nil {
			b.groupReply(ctx, chat, m, t(lang, "cmd_failed", tg.Esc(err.Error())))
			return
		}
		record("mute")
		b.groupReply(ctx, chat, m, t(lang, "mute_done", who(), humanDuration(d, lang)))
	case "unmute":
		if !needTarget() {
			return
		}
		if err := b.tg.RestrictChatMember(ctx, chat.ID, targetUser.ID, tg.AllPermissions(true), time.Time{}); err != nil {
			b.groupReply(ctx, chat, m, t(lang, "cmd_failed", tg.Esc(err.Error())))
			return
		}
		record("unmute")
		b.groupReply(ctx, chat, m, t(lang, "unmute_done", who()))
	case "unban":
		if targetUser == nil {
			b.groupReply(ctx, chat, m, t(lang, "cmd_need_reply"))
			return
		}
		if err := b.tg.UnbanChatMember(ctx, chat.ID, targetUser.ID, true); err != nil {
			b.groupReply(ctx, chat, m, t(lang, "cmd_failed", tg.Esc(err.Error())))
			return
		}
		record("unban")
		b.groupReply(ctx, chat, m, t(lang, "unban_done", who()))
	}
}

func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// sendGroupPanelTo opens the group's settings panel in the admin's private chat.
func (b *Bot) sendGroupPanelTo(ctx context.Context, chat *store.Chat, m *tg.Message) {
	lang := chat.Lang(b.lang)
	s := b.groupScreen(ctx, chat, m.From.ID, b.lang)
	if _, err := b.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: m.From.ID, Text: s.text, ReplyMarkup: s.kb}); err != nil {
		link := "https://t.me/" + b.Me.Username + "?start=g" + strconv.FormatInt(chat.ID, 10)
		p := tg.SendMessageParams{ChatID: chat.ID, Text: t(lang, "settings_start_bot"), DisableNotif: true,
			ReplyMarkup: tg.Keyboard(tg.Row(tg.URLButton(t(lang, "btn_open_private"), link)))}
		if sent, err := b.tg.SendMessage(ctx, p); err == nil {
			_ = b.st.ScheduleDeletion(ctx, store.Deletion{ChatID: chat.ID, MessageID: sent.MessageID, Due: time.Now().Add(time.Minute)})
		}
		return
	}
	b.groupReply(ctx, chat, m, t(lang, "settings_sent"))
}

func (b *Bot) groupStatusLine(ctx context.Context, chat *store.Chat, lang string) string {
	days, _ := b.st.Stats(ctx, chat.ID, 1, b.cfg.Location)
	var joins, passed int
	if len(days) > 0 {
		joins, passed = days[0].Counts[store.EvJoin], days[0].Counts[store.EvPass]+days[0].Counts[store.EvApprove]
	}
	open, _ := b.st.CountOpenGates(ctx)
	status := t(lang, "status_"+chat.Status)
	if chat.InLockdown(time.Now()) {
		status += " · " + t(lang, "status_lockdown")
	}
	return t(lang, "group_status", status, joins, passed, open[chat.ID])
}

// validSpan reports whether d is a restriction length Telegram honours as
// given (anything under 30 seconds or over 366 days means "forever").
func validSpan(d time.Duration) bool {
	return d >= time.Minute && d <= 366*24*time.Hour
}
