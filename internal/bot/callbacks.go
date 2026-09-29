package bot

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"tadmin/internal/store"
	"tadmin/internal/tg"
)

func (b *Bot) answer(ctx context.Context, cq *tg.CallbackQuery, text string, alert bool) {
	_ = b.tg.AnswerCallback(ctx, tg.AnswerCallbackParams{CallbackQueryID: cq.ID, Text: text, ShowAlert: alert})
}

// memo keeps long values (URLs) addressable from 64-byte callback data.
type memo struct {
	mu   sync.Mutex
	seq  int
	vals map[string]string
	keys []string
}

var memos = &memo{vals: map[string]string{}}

func (b *Bot) remember(v string) string {
	memos.mu.Lock()
	defer memos.mu.Unlock()
	memos.seq++
	k := strconv.FormatInt(int64(memos.seq), 36)
	memos.vals[k] = v
	memos.keys = append(memos.keys, k)
	if len(memos.keys) > 500 {
		delete(memos.vals, memos.keys[0])
		memos.keys = memos.keys[1:]
	}
	return k
}

func (b *Bot) recall(k string) (string, bool) {
	memos.mu.Lock()
	defer memos.mu.Unlock()
	v, ok := memos.vals[k]
	return v, ok
}

func (b *Bot) onCallback(ctx context.Context, cq *tg.CallbackQuery) {
	if b.Gate.OnCallback(ctx, cq) {
		return
	}
	parts := strings.Split(cq.Data, ":")
	lang := b.userLang(&cq.From)
	if cq.Message == nil {
		b.answer(ctx, cq, "", false)
		return
	}
	msgChat, msgID := cq.Message.Chat.ID, cq.Message.MessageID
	id := func(i int) int64 {
		if i < len(parts) {
			v, _ := strconv.ParseInt(parts[i], 10, 64)
			return v
		}
		return 0
	}
	arg := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	switch parts[0] {
	case "au":
		b.authorize(ctx, cq, arg(1) == "ok", id(2))
		return
	case "rl":
		chat, ok := b.chatFor(ctx, id(1), cq.From.ID)
		if !ok {
			b.answer(ctx, cq, t(lang, "cb_admin_only_panel"), true)
			return
		}
		_ = b.Gate.LiftLockdown(ctx, chat.ID)
		b.answer(ctx, cq, t(lang, "raid_lifted"), false)
		if cq.Message.Chat.Type == tg.ChatPrivate {
			chat.LockdownUntil = time.Time{}
			b.show(ctx, msgChat, msgID, b.securityScreen(ctx, chat, lang))
		} else {
			_ = b.tg.EditMessageReplyMarkup(ctx, msgChat, msgID, nil)
		}
		return
	case "dm":
		b.onDemo(ctx, cq)
		return
	}
	// Everything below is the private control panel.
	if cq.Message.Chat.Type != tg.ChatPrivate {
		b.answer(ctx, cq, "", false)
		return
	}
	switch parts[0] {
	case "p":
		b.onPanel(ctx, cq, parts, msgID)
	case "s":
		b.onSetting(ctx, cq, id(1), arg(2), arg(3), msgID)
	case "q":
		b.onPending(ctx, cq, id(1), arg(2), msgID)
	case "pv":
		if !b.isOwner(cq.From.ID) {
			b.answer(ctx, cq, t(lang, "owner_only"), true)
			return
		}
		b.onPreviewAction(ctx, cq, parts, msgID)
	case "ub", "kp":
		if !b.isOwner(cq.From.ID) {
			b.answer(ctx, cq, t(lang, "owner_only"), true)
			return
		}
		b.onUserbotAction(ctx, cq, parts, msgID)
	case "sys":
		if !b.isOwner(cq.From.ID) {
			b.answer(ctx, cq, t(lang, "owner_only"), true)
			return
		}
		b.onSystemAction(ctx, cq, arg(1), msgID)
	default:
		b.answer(ctx, cq, "", false)
	}
}

// onPanel navigates between panel pages.
func (b *Bot) onPanel(ctx context.Context, cq *tg.CallbackQuery, parts []string, msgID int) {
	user := cq.From
	lang := b.userLang(&user)
	if len(parts) < 2 {
		b.answer(ctx, cq, "", false)
		return
	}
	page := parts[1]
	var chatID int64
	if len(parts) > 2 {
		chatID, _ = strconv.ParseInt(parts[2], 10, 64)
	}
	ownerPages := map[string]bool{"home": false, "groups": false, "stats": true, "pv": true, "pvl": true, "pvs": true,
		"pvadd": true, "pvrec": true, "pvclr": true, "pvclrok": true, "ub": true, "sys": true, "del": true, "delok": true}
	if ownerPages[page] && !b.isOwner(user.ID) {
		b.answer(ctx, cq, t(lang, "owner_only"), true)
		return
	}
	var chat *store.Chat
	if !strings.HasPrefix(page, "pv") && !ownerPages[page] && page != "home" && page != "groups" && page != "stats" {
		c, ok := b.chatFor(ctx, chatID, user.ID)
		if !ok {
			b.answer(ctx, cq, t(lang, "cb_admin_only_panel"), true)
			return
		}
		chat = c
	} else if page == "del" || page == "delok" {
		c, err := b.st.GetChat(ctx, chatID)
		if err != nil {
			b.answer(ctx, cq, t(lang, "cb_expired_panel"), false)
			return
		}
		chat = c
	}
	if (page == "cclean" || page == "call" || page == "callok") && !b.canPin(ctx, chat.ID, user.ID) {
		b.answer(ctx, cq, t(lang, "need_pin_right"), true)
		return
	}
	b.answer(ctx, cq, "", false)
	var s screen
	switch page {
	case "home":
		b.sessions.clear(user.ID)
		b.openHome(ctx, &user, msgID)
		return
	case "groups":
		s = b.groupsScreen(ctx, user.ID, lang)
	case "stats":
		s = b.statsScreen(ctx, nil, lang)
	case "g":
		b.sessions.clear(user.ID)
		s = b.groupScreen(ctx, chat, user.ID, lang)
	case "v":
		s = b.verifyScreen(ctx, chat, lang)
	case "w":
		b.sessions.clear(user.ID)
		s = b.welcomeScreen(ctx, chat, lang, "")
	case "wt", "wb":
		kind := map[string]string{"wt": "welcome_text", "wb": "welcome_buttons"}[page]
		b.sessions.set(user.ID, &session{kind: kind, chatID: chat.ID})
		s = screen{text: t(lang, "input_"+kind), kb: tg.Keyboard(tg.Row(back(lang, "p:w:"+itoa(chat.ID))))}
	case "wp":
		b.previewWelcome(ctx, chat, &user, lang)
		return
	case "c":
		s = b.channelScreen(ctx, chat, lang, "")
	case "cclean":
		b.show(ctx, user.ID, msgID, screen{text: t(lang, "unpin_running")})
		res, err := b.Channel.CleanPins(ctx, chat)
		note := t(lang, "unpin_done", res.Unpinned)
		if err != nil {
			note = t(lang, "unpin_failed", tg.Esc(err.Error()))
		} else if res.StoppedAt == "admin" {
			note += "\n" + t(lang, "unpin_kept_admin")
		}
		s = b.channelScreen(ctx, chat, lang, note)
	case "call":
		s = screen{text: t(lang, "unpin_all_confirm", tg.Esc(chat.Title)), kb: tg.Keyboard(tg.Row(
			tg.CBStyle(t(lang, "btn_confirm"), "p:callok:"+itoa(chat.ID), tg.StyleDanger), back(lang, "p:c:"+itoa(chat.ID))))}
	case "callok":
		note := t(lang, "unpin_all_done")
		if err := b.Channel.UnpinEverything(ctx, chat); err != nil {
			note = t(lang, "unpin_failed", tg.Esc(err.Error()))
		}
		s = b.channelScreen(ctx, chat, lang, note)
	case "cl":
		s = b.cleanScreen(ctx, chat, lang)
	case "sec":
		s = b.securityScreen(ctx, chat, lang)
	case "q":
		s = b.pendingScreen(ctx, chat, lang)
	case "st":
		s = b.statsScreen(ctx, chat, lang)
	case "pause":
		next := store.ChatPaused
		switch chat.Status {
		case store.ChatPaused:
			next = store.ChatActive
		case store.ChatActive:
		default: // pending chats are authorized only via the owner card
			next = chat.Status
		}
		_ = b.st.SetChatStatus(ctx, chat.ID, next)
		chat.Status = next
		s = b.groupScreen(ctx, chat, user.ID, lang)
	case "chk":
		rep := b.refreshChat(ctx, chat.ID)
		chat, _ = b.st.GetChat(ctx, chat.ID)
		s = screen{text: t(lang, "chk_title", tg.Esc(chat.Title)) + "\n\n" + strings.Join(rep.lines, "\n"),
			kb: tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_recheck"), "p:chk:"+itoa(chat.ID)), back(lang, "p:g:"+itoa(chat.ID))))}
		if rep.fatal {
			s.text += "\n\n" + t(lang, "setup_fix")
		}
	case "demo":
		b.sendDemo(ctx, &user, chat, lang)
		return
	case "del":
		s = screen{text: t(lang, "del_confirm", tg.Esc(chat.Title)), kb: tg.Keyboard(tg.Row(
			tg.CBStyle(t(lang, "btn_confirm_remove"), "p:delok:"+itoa(chat.ID), tg.StyleDanger), back(lang, "p:g:"+itoa(chat.ID))))}
	case "delok":
		_ = b.tg.LeaveChat(ctx, chat.ID)
		_ = b.st.DeleteChat(ctx, chat.ID)
		s = b.groupsScreen(ctx, user.ID, lang)
		s.text = t(lang, "del_done", tg.Esc(chat.Title)) + "\n\n" + s.text
	case "pv":
		s = b.previewScreen(ctx)
	case "pvl":
		s = b.sourcesScreen(ctx)
	case "pvs":
		s = b.sourceScreen(ctx, chatID, "")
	case "pvadd":
		b.sessions.set(user.ID, &session{kind: "add_source"})
		s = screen{text: t(lang, "pv_add_prompt"), kb: tg.Keyboard(tg.Row(back(lang, "p:pv")))}
	case "pvrec":
		s = b.recentScreen(ctx)
	case "pvclr":
		s = screen{text: t(lang, "pv_clear_confirm"), kb: tg.Keyboard(tg.Row(tg.CBStyle(t(lang, "btn_confirm"), "p:pvclrok", tg.StyleDanger), back(lang, "p:pv")))}
	case "pvclrok":
		n, _ := b.st.ClearQueue(ctx)
		s = b.previewScreen(ctx)
		s.text = t(lang, "pv_cleared", n) + "\n\n" + s.text
	case "ub":
		s = b.userbotScreen(ctx, "")
	case "sys":
		s = b.systemScreen(ctx)
	default:
		return
	}
	b.show(ctx, user.ID, msgID, s)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
