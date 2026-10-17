package bot

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"tadmin/internal/tg"
	"tadmin/internal/userbot"
)

func (b *Bot) previewScreen(ctx context.Context) screen {
	lang := b.lang
	st := b.Preview.Status(ctx)
	var sb strings.Builder
	sb.WriteString(t(lang, "pv_title"))
	sb.WriteString("\n\n")
	switch {
	case b.Userbot == nil:
		sb.WriteString(t(lang, "pv_userbot_unconfigured"))
	default:
		us := b.Userbot.Status()
		if us.State == userbot.StateReady && us.Self != nil {
			sb.WriteString(t(lang, "pv_userbot_ready", userLabel(us.Self)))
		} else {
			sb.WriteString(t(lang, "pv_userbot_state", t(lang, "ub_state_"+string(us.State))))
		}
	}
	sb.WriteString("\n" + t(lang, "pv_stats", st.Sources, st.Queue.Tracked, st.Queue.Queued, st.Queue.DoneSince, st.DailyLimit, st.Queue.FailedSince))
	if d := time.Until(st.FloodUntil); d > 0 {
		sb.WriteString("\n" + t(lang, "pv_flood_active", humanDuration(d+time.Minute, lang)))
	} else if st.Ready && st.Queue.Queued > 0 {
		if d := time.Until(st.NextAt); d > 0 {
			sb.WriteString("\n" + t(lang, "pv_next", int(d.Seconds())+1))
		}
	}
	sb.WriteString("\n\n" + t(lang, "pv_hint"))
	rows := [][]tg.Button{
		tg.Row(tg.CB(t(lang, "btn_sources"), "p:pvl"), tg.CBStyle(t(lang, "btn_add_source"), "p:pvadd", tg.StylePrimary)),
		tg.Row(tg.CB(t(lang, "btn_recent"), "p:pvrec"), tg.CB(t(lang, "btn_clear_queue"), "p:pvclr")),
	}
	if b.Userbot != nil {
		rows = append(rows, tg.Row(tg.CB(t(lang, "btn_userbot"), "p:ub")))
	}
	rows = append(rows, tg.Row(tg.CB(t(lang, "btn_refresh"), "p:pv"), back(lang, "p:home")))
	return screen{text: sb.String(), kb: &tg.InlineKeyboard{InlineKeyboard: rows}}
}

func userLabel(s *userbot.Self) string {
	name := strings.TrimSpace(s.FirstName + " " + s.LastName)
	if s.Username != "" {
		return tg.Esc(name) + " (@" + tg.Esc(s.Username) + ")"
	}
	return tg.Esc(name) + " " + tg.Esc(s.PhoneMasked)
}

func (b *Bot) sourcesScreen(ctx context.Context) screen {
	lang := b.lang
	srcs, _ := b.st.ListSources(ctx)
	var rows [][]tg.Button
	for _, s := range srcs {
		icon := "🟢"
		switch {
		case !s.Enabled:
			icon = "⏸"
		case s.LastError != "":
			icon = "⚠️"
		}
		label := s.Title
		if label == "" {
			label = strings.TrimPrefix(strings.TrimPrefix(s.URL, "https://"), "http://")
		}
		rows = append(rows, tg.Row(tg.CB(fmt.Sprintf("%s %s · %d", icon, tg.Truncate(label, 30), s.URLCount), "p:pvs:"+itoa(s.ID))))
	}
	rows = append(rows, tg.Row(tg.CBStyle(t(lang, "btn_add_source"), "p:pvadd", tg.StylePrimary), back(lang, "p:pv")))
	text := t(lang, "pv_sources_title", len(srcs))
	if len(srcs) == 0 {
		text += "\n\n" + t(lang, "pv_sources_empty")
	}
	return screen{text: text, kb: &tg.InlineKeyboard{InlineKeyboard: rows}}
}

func (b *Bot) sourceScreen(ctx context.Context, id int64, note string) screen {
	lang := b.lang
	s, err := b.st.GetSource(ctx, id)
	if err != nil {
		return b.sourcesScreen(ctx)
	}
	var sb strings.Builder
	sb.WriteString(t(lang, "pv_source_title", tg.Esc(c0(s.Title, t(lang, "kind_src_"+s.Kind)))))
	sb.WriteString("\n🔗 " + tg.Esc(s.URL))
	sb.WriteString("\n" + t(lang, "pv_source_info", t(lang, "kind_src_"+s.Kind), s.URLCount))
	if !s.CheckedAt.IsZero() {
		sb.WriteString("\n" + t(lang, "pv_source_checked", s.CheckedAt.In(b.cfg.Location).Format("01-02 15:04"), s.NextCheck.In(b.cfg.Location).Format("01-02 15:04")))
	}
	if s.LastError != "" {
		sb.WriteString("\n⚠️ " + tg.Esc(tg.Truncate(s.LastError, 200)))
	}
	if note != "" {
		sb.WriteString("\n\n" + note)
	}
	sid := itoa(s.ID)
	enable := t(lang, "btn_pause")
	if !s.Enabled {
		enable = t(lang, "btn_resume")
	}
	kb := tg.Keyboard(
		tg.Row(tg.CBStyle(t(lang, "btn_check_now"), "pv:chk:"+sid, tg.StylePrimary), tg.CB(t(lang, "btn_queue_all"), "pv:qall:"+sid)),
		tg.Row(tg.CB(enable, "pv:en:"+sid), tg.CB(t(lang, "btn_delete"), "pv:del:"+sid)),
		tg.Row(back(lang, "p:pvl")),
	)
	return screen{text: sb.String(), kb: kb}
}

func (b *Bot) recentScreen(ctx context.Context) screen {
	lang := b.lang
	rec, _ := b.st.RecentRefreshes(ctx, 15)
	var sb strings.Builder
	sb.WriteString(t(lang, "pv_recent_title"))
	if len(rec) == 0 {
		sb.WriteString("\n\n" + t(lang, "pv_recent_empty"))
	}
	for _, r := range rec {
		icon := map[string]string{"updated": "✅", "no_preview": "🚫", "unknown": "❔", "dedup": "♻️", "error": "⚠️"}[r.Result]
		if icon == "" {
			icon = "•"
		}
		sb.WriteString(fmt.Sprintf("\n%s <code>%s</code> %s", icon, r.RefreshedAt.In(b.cfg.Location).Format("01-02 15:04"), tg.Esc(tg.Truncate(r.URL, 60))))
	}
	return screen{text: sb.String(), kb: tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_refresh"), "p:pvrec"), back(lang, "p:pv")))}
}

// discoverSources finds feed/sitemap candidates for a URL and offers them.
func (b *Bot) discoverSources(ctx context.Context, userID int64, u string) {
	lang := b.lang
	msgID := b.show(ctx, userID, 0, screen{text: t(lang, "pv_discovering", tg.Esc(tg.Truncate(u, 120)))})
	dctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cands, err := b.Preview.Discover(dctx, u)
	if err != nil || len(cands) == 0 {
		msg := t(lang, "pv_discover_none")
		if err != nil {
			msg = t(lang, "pv_discover_failed", tg.Esc(err.Error()))
		}
		b.sessions.set(userID, &session{kind: "add_source"})
		b.show(ctx, userID, msgID, screen{text: msg + "\n\n" + t(lang, "pv_add_prompt"), kb: tg.Keyboard(tg.Row(back(lang, "p:pv")))})
		return
	}
	if len(cands) > 8 {
		cands = cands[:8]
	}
	b.sessions.set(userID, &session{kind: "pick_source", cands: cands})
	var sb strings.Builder
	sb.WriteString(t(lang, "pv_discover_found", len(cands)))
	var rows [][]tg.Button
	for i, c := range cands {
		label := t(lang, "kind_src_"+string(c.Kind))
		sb.WriteString(fmt.Sprintf("\n%d. %s — %s", i+1, label, tg.Esc(tg.Truncate(c.URL, 80))))
		rows = append(rows, tg.Row(tg.CB(fmt.Sprintf("%d. %s", i+1, label), "pv:pick:"+strconv.Itoa(i))))
	}
	rows = append(rows, tg.Row(back(lang, "p:pv")))
	b.show(ctx, userID, msgID, screen{text: sb.String(), kb: &tg.InlineKeyboard{InlineKeyboard: rows}})
}

func (b *Bot) onPreviewAction(ctx context.Context, cq *tg.CallbackQuery, parts []string, msgID int) {
	lang := b.lang
	user := cq.From.ID
	arg := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	id, _ := strconv.ParseInt(arg(2), 10, 64)
	switch arg(1) {
	case "r": // refresh one remembered url
		u, ok := b.recall(arg(2))
		if !ok {
			b.answer(ctx, cq, t(lang, "cb_expired_panel"), false)
			return
		}
		b.answer(ctx, cq, "", false)
		b.refreshOne(ctx, msgID, u)
	case "ra":
		v, ok := b.recall(arg(2))
		if !ok {
			b.answer(ctx, cq, t(lang, "cb_expired_panel"), false)
			return
		}
		b.answer(ctx, cq, "", false)
		b.refreshURLs(ctx, strings.Split(v, "\n"))
	case "d":
		u, ok := b.recall(arg(2))
		if !ok {
			b.answer(ctx, cq, t(lang, "cb_expired_panel"), false)
			return
		}
		b.answer(ctx, cq, "", false)
		b.discoverSources(ctx, user, u)
	case "pick":
		s := b.sessions.get(user)
		i, _ := strconv.Atoi(arg(2))
		if s == nil || s.kind != "pick_source" || i < 0 || i >= len(s.cands) {
			b.answer(ctx, cq, t(lang, "cb_expired_panel"), false)
			return
		}
		b.answer(ctx, cq, "", false)
		c := s.cands[i]
		b.show(ctx, user, msgID, screen{
			text: t(lang, "pv_baseline_q", tg.Esc(c.URL)),
			kb: tg.Keyboard(
				tg.Row(tg.CBStyle(t(lang, "btn_baseline_future"), "pv:base:"+arg(2)+":0", tg.StylePrimary)),
				tg.Row(tg.CB(t(lang, "btn_baseline_all"), "pv:base:"+arg(2)+":1")),
				tg.Row(back(lang, "p:pv")),
			),
		})
	case "base":
		s := b.sessions.get(user)
		i, _ := strconv.Atoi(arg(2))
		if s == nil || s.kind != "pick_source" || i < 0 || i >= len(s.cands) {
			b.answer(ctx, cq, t(lang, "cb_expired_panel"), false)
			return
		}
		b.answer(ctx, cq, "", false)
		c := s.cands[i]
		b.sessions.clear(user)
		b.show(ctx, user, msgID, screen{text: t(lang, "pv_adding", tg.Esc(c.URL))})
		actx, cancel := context.WithTimeout(ctx, 90*time.Second)
		src, r, err := b.Preview.Add(actx, c, arg(3) == "1")
		cancel()
		if src == nil {
			b.show(ctx, user, msgID, screen{text: t(lang, "pv_add_failed", tg.Esc(errString(err))), kb: tg.Keyboard(tg.Row(back(lang, "p:pv")))})
			return
		}
		note := t(lang, "pv_added", r.New, r.Queued)
		if err != nil {
			note = t(lang, "pv_add_failed", tg.Esc(err.Error()))
		}
		b.show(ctx, user, msgID, b.sourceScreen(ctx, src.ID, note))
	case "chk":
		src, err := b.st.GetSource(ctx, id)
		if err != nil {
			b.answer(ctx, cq, t(lang, "cb_expired_panel"), false)
			return
		}
		b.answer(ctx, cq, t(lang, "pv_checking"), false)
		cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		src.ETag, src.LastModified = "", "" // force a full fetch
		r, err := b.Preview.Check(cctx, src)
		cancel()
		note := t(lang, "pv_checked", r.New, r.Changed, r.Queued)
		if err != nil {
			note = "⚠️ " + tg.Esc(err.Error())
		}
		b.show(ctx, user, msgID, b.sourceScreen(ctx, id, note))
	case "qall":
		n, _ := b.st.EnqueueSource(ctx, id)
		b.Preview.Wake()
		b.answer(ctx, cq, t(lang, "pv_queued_n_short", n), false)
		b.show(ctx, user, msgID, b.sourceScreen(ctx, id, t(lang, "pv_queued_n", n)))
	case "en":
		src, err := b.st.GetSource(ctx, id)
		if err == nil {
			_ = b.st.SetSourceEnabled(ctx, id, !src.Enabled)
		}
		b.answer(ctx, cq, t(lang, "saved_short"), false)
		b.show(ctx, user, msgID, b.sourceScreen(ctx, id, ""))
	case "del":
		b.answer(ctx, cq, "", false)
		b.show(ctx, user, msgID, screen{text: t(lang, "pv_delete_confirm"), kb: tg.Keyboard(tg.Row(
			tg.CBStyle(t(lang, "btn_confirm"), "pv:delok:"+arg(2), tg.StyleDanger), back(lang, "p:pvs:"+arg(2))))})
	case "delok":
		_ = b.st.DeleteSource(ctx, id)
		b.answer(ctx, cq, t(lang, "cb_done_generic"), false)
		b.show(ctx, user, msgID, b.sourcesScreen(ctx))
	default:
		b.answer(ctx, cq, "", false)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ---- userbot login ----

func (b *Bot) userbotScreen(ctx context.Context, note string) screen {
	lang := b.lang
	if b.Userbot == nil {
		return screen{text: t(lang, "pv_userbot_unconfigured"), kb: tg.Keyboard(tg.Row(back(lang, "p:pv")))}
	}
	st := b.Userbot.Status()
	var sb strings.Builder
	sb.WriteString(t(lang, "ub_title"))
	sb.WriteString("\n\n" + t(lang, "ub_state", t(lang, "ub_state_"+string(st.State))))
	if st.Self != nil {
		sb.WriteString("\n" + t(lang, "ub_account", userLabel(st.Self)))
	}
	if st.LastError != "" && st.State != userbot.StateReady {
		sb.WriteString("\n⚠️ " + tg.Esc(tg.Truncate(st.LastError, 160)))
	}
	sb.WriteString("\n\n" + t(lang, "ub_explain"))
	if note != "" {
		sb.WriteString("\n\n" + note)
	}
	var rows [][]tg.Button
	switch st.State {
	case userbot.StateReady:
		rows = append(rows, tg.Row(tg.CBStyle(t(lang, "btn_logout"), "ub:logout", tg.StyleDanger)))
	case userbot.StateLoggedOut:
		rows = append(rows, tg.Row(tg.CBStyle(t(lang, "btn_qr_login"), "ub:qr", tg.StylePrimary), tg.CB(t(lang, "btn_phone_login"), "ub:phone")))
	}
	rows = append(rows, tg.Row(tg.CB(t(lang, "btn_refresh"), "p:ub"), back(lang, "p:pv")))
	return screen{text: sb.String(), kb: &tg.InlineKeyboard{InlineKeyboard: rows}}
}

func (b *Bot) baseCtx() context.Context {
	if b.runCtx != nil {
		return b.runCtx
	}
	return context.Background()
}

func (b *Bot) onUserbotAction(ctx context.Context, cq *tg.CallbackQuery, parts []string, msgID int) {
	lang := b.lang
	owner := b.cfg.OwnerID
	if b.Userbot == nil {
		b.answer(ctx, cq, t(lang, "pv_userbot_unconfigured"), true)
		return
	}
	if len(parts) < 2 {
		b.answer(ctx, cq, "", false)
		return
	}
	if parts[0] == "kp" {
		b.onKeypad(ctx, cq, parts[1], msgID)
		return
	}
	switch parts[1] {
	case "qr":
		b.answer(ctx, cq, "", false)
		b.startQR(owner)
	case "phone":
		b.answer(ctx, cq, "", false)
		b.sessions.set(owner, &session{kind: "phone"})
		b.show(ctx, owner, msgID, screen{text: t(lang, "ub_phone_prompt"), kb: tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_cancel"), "ub:cancel")))})
	case "cancel":
		b.answer(ctx, cq, "", false)
		b.cancelLogin()
		b.sessions.clear(owner)
		if len(cq.Message.Photo) > 0 {
			_ = b.tg.DeleteMessage(ctx, owner, msgID)
			msgID = 0
		}
		b.show(ctx, owner, msgID, b.userbotScreen(ctx, t(lang, "ub_cancelled")))
	case "logout":
		b.answer(ctx, cq, "", false)
		note := t(lang, "ub_logged_out")
		if err := b.Userbot.Logout(ctx); err != nil {
			note = "⚠️ " + tg.Esc(err.Error())
		}
		b.show(ctx, owner, msgID, b.userbotScreen(ctx, note))
	default:
		b.answer(ctx, cq, "", false)
	}
}

func (b *Bot) cancelLogin() {
	b.loginMu.Lock()
	if b.qr != nil {
		b.qr.Cancel()
		b.qr = nil
	}
	b.loginMu.Unlock()
	b.Userbot.CancelLogin()
}

// startQR runs a QR login, keeping the QR photo fresh until it is scanned.
func (b *Bot) startQR(owner int64) {
	lang := b.lang
	b.cancelLogin()
	ctx, cancel := context.WithTimeout(b.baseCtx(), 10*time.Minute)
	q, err := b.Userbot.StartQRLogin(ctx)
	if err != nil {
		cancel()
		b.show(context.Background(), owner, 0, b.userbotScreen(context.Background(), "⚠️ "+tg.Esc(loginErr(lang, err))))
		return
	}
	b.loginMu.Lock()
	b.qr = q
	b.loginMu.Unlock()
	updates, results := q.Updates(), q.Result()
	go func() {
		defer cancel()
		msgID := 0
		kb := tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_cancel"), "ub:cancel")))
		cleanup := func() {
			if msgID != 0 {
				_ = b.tg.DeleteMessage(context.Background(), owner, msgID)
			}
		}
		for {
			select {
			case u, ok := <-updates:
				if !ok {
					updates = nil
					continue
				}
				caption := t(lang, "ub_qr_caption", u.Expires.In(b.cfg.Location).Format("15:04:05"))
				if msgID == 0 {
					if m, err := b.tg.SendPhoto(ctx, tg.SendPhotoParams{ChatID: owner, Caption: caption, ReplyMarkup: kb}, u.PNG); err == nil {
						msgID = m.MessageID
					}
				} else {
					_ = b.tg.EditMessagePhoto(ctx, owner, msgID, u.PNG, caption, kb)
				}
			case res := <-results:
				cleanup()
				b.loginMu.Lock()
				if b.qr == q {
					b.qr = nil
				}
				b.loginMu.Unlock()
				b.afterLogin(context.Background(), res.LoginResult, res.Err)
				return
			case <-ctx.Done():
				cleanup()
				b.loginMu.Lock()
				current := b.qr == q
				if current {
					b.qr = nil
				}
				b.loginMu.Unlock()
				if current && b.baseCtx().Err() == nil {
					b.show(context.Background(), owner, 0, b.userbotScreen(context.Background(), t(lang, "ub_qr_expired")))
				}
				return
			}
		}
	}()
}

func (b *Bot) afterLogin(ctx context.Context, r userbot.LoginResult, err error) {
	lang := b.lang
	owner := b.cfg.OwnerID
	switch {
	case errors.Is(err, userbot.ErrLoginCancelled):
		return
	case err != nil:
		b.show(ctx, owner, 0, b.userbotScreen(ctx, "⚠️ "+tg.Esc(loginErr(lang, err))))
	case r.NeedPassword:
		b.sessions.set(owner, &session{kind: "password"})
		hint := ""
		if r.PasswordHint != "" {
			hint = t(lang, "ub_password_hint", tg.Esc(r.PasswordHint))
		}
		b.show(ctx, owner, 0, screen{text: t(lang, "ub_password_prompt") + hint, kb: tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_cancel"), "ub:cancel")))})
	default:
		b.sessions.clear(owner)
		name := ""
		if r.Self != nil {
			name = userLabel(r.Self)
		}
		b.show(ctx, owner, 0, b.userbotScreen(ctx, t(lang, "ub_login_ok", name)))
	}
}

func loginErr(lang string, err error) string {
	for _, k := range []struct {
		e   error
		key string
	}{
		{userbot.ErrInvalidCode, "ub_err_code"}, {userbot.ErrCodeExpired, "ub_err_code_expired"},
		{userbot.ErrPasswordInvalid, "ub_err_password"}, {userbot.ErrPhoneInvalid, "ub_err_phone"},
		{userbot.ErrPhoneBanned, "ub_err_banned"}, {userbot.ErrSignUpRequired, "ub_err_signup"},
		{userbot.ErrAlreadyLoggedIn, "ub_err_already"}, {userbot.ErrNotReady, "ub_err_offline"},
		{userbot.ErrTimeout, "ub_err_timeout"},
	} {
		if errors.Is(err, k.e) {
			return t(lang, k.key)
		}
	}
	var fw *userbot.FloodWaitError
	if errors.As(err, &fw) {
		return t(lang, "pv_flood", humanDuration(fw.Wait+time.Minute, lang))
	}
	return err.Error()
}

func (b *Bot) startPhoneLogin(ctx context.Context, phone string) {
	lang := b.lang
	owner := b.cfg.OwnerID
	lctx, cancel := context.WithTimeout(b.baseCtx(), time.Minute)
	defer cancel()
	info, err := b.Userbot.StartPhoneLogin(lctx, phone)
	if err != nil {
		b.sessions.set(owner, &session{kind: "phone"})
		b.show(ctx, owner, 0, screen{text: "⚠️ " + tg.Esc(loginErr(lang, err)) + "\n\n" + t(lang, "ub_phone_prompt"),
			kb: tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_cancel"), "ub:cancel")))})
		return
	}
	s := &session{kind: "code", chatID: int64(info.Length)}
	b.sessions.set(owner, s)
	s.msgID = b.show(ctx, owner, 0, b.keypadScreen(s, t(lang, "ub_code_sent_"+codeType(info.Type))))
}

func codeType(t string) string {
	switch t {
	case "app", "sms":
		return t
	}
	return "other"
}

func (b *Bot) keypadScreen(s *session, note string) screen {
	lang := b.lang
	n := int(s.chatID)
	if n <= 0 {
		n = 5
	}
	var dots []string
	for i := 0; i < max(n, len(s.code)); i++ {
		if i < len(s.code) {
			dots = append(dots, "●")
		} else {
			dots = append(dots, "○")
		}
	}
	text := t(lang, "ub_keypad") + "\n\n<code>" + strings.Join(dots, " ") + "</code>"
	if note != "" {
		text = note + "\n\n" + text
	}
	d := func(x string) tg.Button { return tg.CB(x, "kp:"+x) }
	kb := tg.Keyboard(
		tg.Row(d("1"), d("2"), d("3")),
		tg.Row(d("4"), d("5"), d("6")),
		tg.Row(d("7"), d("8"), d("9")),
		tg.Row(tg.CB("⌫", "kp:del"), d("0"), tg.CBStyle("✅", "kp:ok", tg.StyleSuccess)),
		tg.Row(tg.CB(t(lang, "btn_cancel"), "ub:cancel")),
	)
	return screen{text: text, kb: kb}
}

// onKeypad collects the login code from inline digit buttons, so the code
// never appears as a chat message (Telegram rejects codes that were shared).
func (b *Bot) onKeypad(ctx context.Context, cq *tg.CallbackQuery, key string, msgID int) {
	lang := b.lang
	owner := b.cfg.OwnerID
	s := b.sessions.get(owner)
	if s == nil || s.kind != "code" || s.msgID != msgID {
		b.answer(ctx, cq, t(lang, "cb_expired_panel"), false)
		return
	}
	b.answer(ctx, cq, "", false)
	switch key {
	case "del":
		if len(s.code) > 0 {
			s.code = s.code[:len(s.code)-1]
		}
	case "ok":
	default:
		if len(s.code) < 8 && len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
			s.code += key
		}
	}
	want := int(s.chatID)
	if key != "ok" && (want <= 0 || len(s.code) < want) {
		b.show(ctx, owner, msgID, b.keypadScreen(s, ""))
		return
	}
	if len(s.code) < 4 {
		b.show(ctx, owner, msgID, b.keypadScreen(s, ""))
		return
	}
	code := s.code
	s.code = ""
	b.show(ctx, owner, msgID, screen{text: t(lang, "ub_checking")})
	lctx, cancel := context.WithTimeout(b.baseCtx(), time.Minute)
	defer cancel()
	r, err := b.Userbot.SubmitCode(lctx, code)
	if errors.Is(err, userbot.ErrInvalidCode) {
		b.show(ctx, owner, msgID, b.keypadScreen(s, "⚠️ "+t(lang, "ub_err_code")))
		return
	}
	_ = b.tg.DeleteMessage(ctx, owner, msgID)
	b.afterLogin(ctx, r, err)
}

func (b *Bot) submitPassword(ctx context.Context, pwd string) {
	lang := b.lang
	owner := b.cfg.OwnerID
	lctx, cancel := context.WithTimeout(b.baseCtx(), time.Minute)
	defer cancel()
	r, err := b.Userbot.SubmitPassword(lctx, pwd)
	if errors.Is(err, userbot.ErrPasswordInvalid) {
		b.sessions.set(owner, &session{kind: "password"})
		b.show(ctx, owner, 0, screen{text: "⚠️ " + t(lang, "ub_err_password") + "\n\n" + t(lang, "ub_password_prompt"),
			kb: tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_cancel"), "ub:cancel")))})
		return
	}
	b.afterLogin(ctx, r, err)
}

// ---- system ----

func (b *Bot) systemScreen(ctx context.Context) screen {
	lang := b.lang
	size, _ := b.st.Size(ctx)
	var sb strings.Builder
	sb.WriteString(t(lang, "sys_title"))
	sb.WriteString("\n\n" + t(lang, "sys_version", tg.Esc(b.Version), runtime.Version(), humanDuration(time.Since(b.Started), lang)))
	sb.WriteString("\n" + t(lang, "sys_db", humanBytes(size), tg.Esc(b.cfg.DatabasePath())))
	if b.WebReady {
		sb.WriteString("\n" + t(lang, "sys_web_on", tg.Esc(b.cfg.Web.PublicURL)))
	} else {
		sb.WriteString("\n" + t(lang, "sys_web_off"))
	}
	sb.WriteString("\n" + t(lang, "sys_ai", onOff(lang, b.AIReady)))
	ub := t(lang, "ub_state_disabled")
	if b.Userbot != nil {
		ub = t(lang, "ub_state_"+string(b.Userbot.Status().State))
	}
	sb.WriteString("\n" + t(lang, "sys_userbot", ub))
	sb.WriteString("\n" + t(lang, "sys_tz", tg.Esc(b.cfg.Location.String())))
	kb := tg.Keyboard(
		tg.Row(tg.CBStyle(t(lang, "btn_backup"), "sys:backup", tg.StylePrimary), tg.CB(t(lang, "btn_commands"), "sys:cmds")),
		tg.Row(back(lang, "p:home")),
	)
	return screen{text: sb.String(), kb: kb}
}

func (b *Bot) onSystemAction(ctx context.Context, cq *tg.CallbackQuery, action string, msgID int) {
	lang := b.lang
	switch action {
	case "backup":
		b.answer(ctx, cq, t(lang, "backup_running"), false)
		b.sendBackup(ctx)
	case "cmds":
		b.RegisterCommands(ctx)
		b.answer(ctx, cq, t(lang, "cmds_updated"), false)
	default:
		b.answer(ctx, cq, "", false)
	}
	_ = msgID
}
