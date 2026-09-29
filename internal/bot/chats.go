package bot

import (
	"context"
	"strconv"
	"strings"

	"tadmin/internal/store"
	"tadmin/internal/tg"
)

// checkReport is the result of a permission check.
type checkReport struct {
	lines    []string
	problems []string // short problem codes, stored on the chat
	fatal    bool     // verification cannot work
	info     *tg.ChatFullInfo
}

// checkChat inspects the bot's rights and relevant chat settings.
func (b *Bot) checkChat(ctx context.Context, chatID int64, lang string) checkReport {
	var r checkReport
	add := func(ok bool, warnOnly bool, key string, args ...any) {
		mark := "✅"
		if !ok {
			if warnOnly {
				mark = "⚠️"
			} else {
				mark = "❌"
				r.fatal = true
			}
			r.problems = append(r.problems, key)
		}
		r.lines = append(r.lines, mark+" "+t(lang, key, args...))
	}
	info, err := b.tg.GetChat(ctx, chatID)
	if err != nil {
		r.fatal = true
		r.problems = append(r.problems, "chk_unreachable")
		r.lines = append(r.lines, "❌ "+t(lang, "chk_unreachable"))
		return r
	}
	r.info = info
	add(info.Type == tg.ChatSupergroup, false, "chk_supergroup")
	me, err := b.tg.GetChatMember(ctx, chatID, b.Me.ID)
	if err != nil {
		add(false, false, "chk_admin")
		return r
	}
	add(me.IsAdmin(), false, "chk_admin")
	isCreator := me.Status == tg.StatusCreator
	add(isCreator || me.CanRestrictMembers, false, "chk_restrict")
	add(isCreator || me.CanDeleteMessages, true, "chk_delete")
	add(isCreator || (me.CanPinMessages != nil && *me.CanPinMessages), true, "chk_pin")
	add(isCreator || (me.CanInviteUsers != nil && *me.CanInviteUsers), true, "chk_invite")
	if info.LinkedChatID != 0 {
		add(info.JoinToSendMessages, true, "chk_join_to_send")
	}
	return r
}

// onMyChatMember handles the bot being added, promoted, demoted or removed.
func (b *Bot) onMyChatMember(ctx context.Context, u *tg.ChatMemberUpdated) {
	if u.Chat.Type != tg.ChatGroup && u.Chat.Type != tg.ChatSupergroup {
		return
	}
	b.Roles.Invalidate(u.Chat.ID)
	existing, _ := b.st.GetChat(ctx, u.Chat.ID)
	lang := b.lang
	if !u.NewChatMember.IsPresent() {
		if existing != nil && existing.Status != store.ChatLeft {
			_ = b.st.SetChatStatus(ctx, u.Chat.ID, store.ChatLeft)
			b.notifyOwner(ctx, t(lang, "chat_removed", tg.Esc(u.Chat.Title), tg.Mention(&u.From)), nil)
		}
		return
	}
	fresh := existing == nil || existing.Status == store.ChatLeft
	status := store.ChatActive
	if fresh && !b.isOwner(u.From.ID) {
		status = store.ChatPending
	} else if existing != nil && !fresh {
		status = existing.Status
	}
	c := &store.Chat{ID: u.Chat.ID, Title: u.Chat.Title, Username: u.Chat.Username, Status: status, AddedBy: u.From.ID}
	if fresh {
		c.Settings.Language = b.lang
	}
	if err := b.st.UpsertChat(ctx, c); err != nil {
		b.log.Error("upsert chat", "chat", u.Chat.ID, "error", err)
		return
	}
	if status == store.ChatPending {
		b.notifyOwner(ctx, t(lang, "chat_auth_request", tg.Mention(&u.From), tg.Esc(u.Chat.Title), u.Chat.ID),
			tg.Keyboard(tg.Row(
				tg.CBStyle(t(lang, "btn_authorize"), "au:ok:"+strconv.FormatInt(u.Chat.ID, 10), tg.StyleSuccess),
				tg.CBStyle(t(lang, "btn_leave"), "au:no:"+strconv.FormatInt(u.Chat.ID, 10), tg.StyleDanger))))
		return
	}
	rep := b.refreshChat(ctx, u.Chat.ID)
	if fresh {
		b.sendSetupCard(ctx, u.Chat.ID, u.Chat.Title, rep)
		if !u.NewChatMember.IsAdmin() {
			_, _ = b.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: u.Chat.ID, Text: t(lang, "group_need_admin")})
		}
		return
	}
	// Rights changed on a managed chat: tell the owner only about problems.
	if existing != nil && strings.Join(rep.problems, ",") != existing.Problems {
		if len(rep.problems) > 0 {
			b.notifyOwner(ctx, t(lang, "chat_rights_changed", tg.Esc(u.Chat.Title))+"\n\n"+strings.Join(rep.lines, "\n"),
				tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_open_settings"), "p:g:"+strconv.FormatInt(u.Chat.ID, 10)))))
		} else {
			b.notifyOwner(ctx, t(lang, "chat_rights_ok", tg.Esc(u.Chat.Title)), nil)
		}
	}
}

// refreshChat re-checks a chat and stores title, linked channel and problems.
func (b *Bot) refreshChat(ctx context.Context, chatID int64) checkReport {
	lang := b.lang
	if c, err := b.st.GetChat(ctx, chatID); err == nil {
		lang = c.Lang(b.lang)
	}
	rep := b.checkChat(ctx, chatID, lang)
	if rep.info != nil {
		_ = b.st.SetChatInfo(ctx, chatID, rep.info.Title, rep.info.Username, rep.info.LinkedChatID)
	}
	_ = b.st.SetChatProblems(ctx, chatID, strings.Join(rep.problems, ","))
	return rep
}

func (b *Bot) sendSetupCard(ctx context.Context, chatID int64, title string, rep checkReport) {
	lang := b.lang
	text := t(lang, "setup_card", tg.Esc(title)) + "\n\n" + strings.Join(rep.lines, "\n") + "\n\n" + t(lang, "setup_defaults")
	if rep.fatal {
		text += "\n\n" + t(lang, "setup_fix")
	}
	id := strconv.FormatInt(chatID, 10)
	b.notifyOwner(ctx, text, tg.Keyboard(
		tg.Row(tg.CBStyle(t(lang, "btn_open_settings"), "p:g:"+id, tg.StylePrimary), tg.CB(t(lang, "btn_recheck"), "p:chk:"+id)),
	))
}

// authorize handles the owner's decision on a group added by someone else.
func (b *Bot) authorize(ctx context.Context, cq *tg.CallbackQuery, ok bool, chatID int64) {
	if !b.isOwner(cq.From.ID) {
		b.answer(ctx, cq, t(b.lang, "owner_only"), true)
		return
	}
	c, err := b.st.GetChat(ctx, chatID)
	if err != nil || c.Status != store.ChatPending {
		b.answer(ctx, cq, t(b.lang, "cb_expired_panel"), false)
		return
	}
	b.answer(ctx, cq, "", false)
	if !ok {
		_ = b.tg.LeaveChat(ctx, chatID)
		_ = b.st.SetChatStatus(ctx, chatID, store.ChatLeft)
		b.show(ctx, cq.Message.Chat.ID, cq.Message.MessageID, screen{text: t(b.lang, "chat_declined", tg.Esc(c.Title))})
		return
	}
	_ = b.st.SetChatStatus(ctx, chatID, store.ChatActive)
	rep := b.refreshChat(ctx, chatID)
	_ = b.tg.DeleteMessage(ctx, cq.Message.Chat.ID, cq.Message.MessageID)
	b.sendSetupCard(ctx, chatID, c.Title, rep)
}
