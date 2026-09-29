package bot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"tadmin/internal/store"
	"tadmin/internal/tg"
	"tadmin/internal/userbot"
)

var urlRe = regexp.MustCompile(`https?://[^\s<>"']+`)

func (b *Bot) onPrivate(ctx context.Context, m *tg.Message) {
	if m.From == nil || m.From.IsBot {
		return
	}
	user := m.From
	lang := b.userLang(user)
	text := strings.TrimSpace(m.Text)
	if s := b.sessions.get(user.ID); s != nil && isSecretSession(s.kind) && b.isOwner(user.ID) {
		// Phone numbers, codes and passwords never stay in the chat, even
		// when they happen to start with "/".
		_ = b.tg.DeleteMessage(ctx, user.ID, m.MessageID)
		switch {
		case text == "/cancel":
			b.cancelLogin()
			b.sessions.clear(user.ID)
			b.show(ctx, user.ID, 0, b.userbotScreen(ctx, t(lang, "ub_cancelled")))
		case s.kind == "code":
			b.show(ctx, user.ID, 0, screen{text: t(lang, "code_use_keypad")})
		default:
			b.onSessionInput(ctx, m, s)
		}
		return
	}
	if m.IsCommand() {
		cmd, _, args := tg.CommandArgs(text)
		b.sessions.clear(user.ID)
		switch cmd {
		case "start":
			b.onStart(ctx, user, args)
		case "panel", "settings":
			b.openHome(ctx, user, 0)
		case "help":
			b.show(ctx, user.ID, 0, screen{text: t(lang, "help_text", b.Me.Username), kb: tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_open_panel"), "p:home")))})
		case "backup":
			if b.isOwner(user.ID) {
				b.sendBackup(ctx)
			}
		case "refresh":
			if b.isOwner(user.ID) {
				if urls := urlRe.FindAllString(args, 20); len(urls) > 0 {
					b.refreshURLs(ctx, urls)
				} else {
					b.show(ctx, user.ID, 0, b.previewScreen(ctx))
				}
			}
		case "cancel":
			b.show(ctx, user.ID, 0, screen{text: t(lang, "cancelled")})
		default:
			b.onStart(ctx, user, "")
		}
		return
	}
	if s := b.sessions.get(user.ID); s != nil {
		b.onSessionInput(ctx, m, s)
		return
	}
	if b.isOwner(user.ID) {
		if urls := urlRe.FindAllString(text+" "+m.Caption, 20); len(urls) > 0 {
			b.urlCard(ctx, urls)
			return
		}
	}
	b.onStart(ctx, user, "")
}

func (b *Bot) userLang(u *tg.User) string {
	if b.isOwner(u.ID) {
		return b.lang
	}
	lc := strings.ToLower(u.LanguageCode)
	if lc == "" {
		return b.lang
	}
	if strings.HasPrefix(lc, "zh") {
		return "zh-CN"
	}
	return "en"
}

func (b *Bot) onStart(ctx context.Context, user *tg.User, payload string) {
	switch {
	case strings.HasPrefix(payload, "v"):
		b.Gate.OnPrivateStart(ctx, user, payload[1:])
		return
	case strings.HasPrefix(payload, "g"):
		if id, err := strconv.ParseInt(payload[1:], 10, 64); err == nil {
			if c, ok := b.chatFor(ctx, id, user.ID); ok {
				b.show(ctx, user.ID, 0, b.groupScreen(ctx, c, user.ID, b.userLang(user)))
				return
			}
		}
	}
	b.openHome(ctx, user, 0)
}

// openHome shows the owner home, an admin's group list, or an intro.
func (b *Bot) openHome(ctx context.Context, user *tg.User, msgID int) {
	if b.isOwner(user.ID) {
		b.show(ctx, user.ID, msgID, b.homeScreen(ctx))
		return
	}
	lang := b.userLang(user)
	chats := b.manageableChats(ctx, user.ID)
	if len(chats) == 0 {
		b.show(ctx, user.ID, msgID, screen{text: t(lang, "intro", b.Me.FirstName)})
		return
	}
	b.show(ctx, user.ID, msgID, b.groupsScreen(ctx, user.ID, lang))
}

// manageableChats lists managed chats the user may configure.
func (b *Bot) manageableChats(ctx context.Context, userID int64) []*store.Chat {
	all, err := b.st.ListChats(ctx, false)
	if err != nil {
		return nil
	}
	var out []*store.Chat
	for _, c := range all {
		if c.Status == store.ChatPending && !b.isOwner(userID) {
			continue
		}
		if b.canManage(ctx, c.ID, userID) {
			out = append(out, c)
		}
	}
	return out
}

func (b *Bot) onSessionInput(ctx context.Context, m *tg.Message, s *session) {
	user := m.From
	lang := b.userLang(user)
	text := strings.TrimSpace(m.Text)
	switch s.kind {
	case "welcome_text", "welcome_buttons":
		if !b.canManage(ctx, s.chatID, user.ID) {
			b.sessions.clear(user.ID)
			return
		}
		if m.Text == "" {
			b.show(ctx, user.ID, 0, screen{text: t(lang, "input_need_text")})
			return
		}
		// Keep the admin's own formatting by converting entities to HTML.
		value := entitiesToHTML(m.Text, m.Entities)
		if text == "-" {
			value = ""
		}
		if _, err := b.st.UpdateSettings(ctx, s.chatID, func(st *store.Settings) {
			if s.kind == "welcome_text" {
				st.Welcome.Text = value
			} else {
				st.Welcome.Buttons = strings.TrimSpace(m.Text)
				if text == "-" {
					st.Welcome.Buttons = ""
				}
			}
			st.Welcome.Enabled = true
		}); err != nil {
			return
		}
		b.sessions.clear(user.ID)
		c, _ := b.st.GetChat(ctx, s.chatID)
		b.show(ctx, user.ID, 0, b.welcomeScreen(ctx, c, lang, t(lang, "saved")))
	case "add_source":
		if !b.isOwner(user.ID) {
			return
		}
		u := urlRe.FindString(text)
		if u == "" {
			if !strings.Contains(text, "://") && strings.Contains(text, ".") {
				u = "https://" + text
			} else {
				b.show(ctx, user.ID, 0, screen{text: t(lang, "pv_need_url")})
				return
			}
		}
		b.discoverSources(ctx, user.ID, u)
	case "phone":
		if !b.isOwner(user.ID) || b.Userbot == nil {
			return
		}
		_ = b.tg.DeleteMessage(ctx, user.ID, m.MessageID)
		b.startPhoneLogin(ctx, text)
	case "password":
		if !b.isOwner(user.ID) || b.Userbot == nil {
			return
		}
		// Never keep the 2FA password in the chat history.
		_ = b.tg.DeleteMessage(ctx, user.ID, m.MessageID)
		b.sessions.clear(user.ID)
		b.submitPassword(ctx, m.Text)
	default:
		b.sessions.clear(user.ID)
	}
}

// entitiesToHTML converts the formatting entities of a message to HTML so
// templates keep bold/italic/links the admin typed.
func entitiesToHTML(text string, ents []tg.MessageEntity) string {
	if len(ents) == 0 {
		return tg.Esc(text)
	}
	u16 := utf16Encode(text)
	type span struct {
		start, end  int
		open, close string
	}
	var spans []span
	for _, e := range ents {
		var o, c string
		switch e.Type {
		case "bold":
			o, c = "<b>", "</b>"
		case "italic":
			o, c = "<i>", "</i>"
		case "underline":
			o, c = "<u>", "</u>"
		case "strikethrough":
			o, c = "<s>", "</s>"
		case "spoiler":
			o, c = "<tg-spoiler>", "</tg-spoiler>"
		case "code":
			o, c = "<code>", "</code>"
		case "pre":
			o, c = "<pre>", "</pre>"
		case "text_link":
			o, c = `<a href="`+tg.Esc(e.URL)+`">`, "</a>"
		case "blockquote", "expandable_blockquote":
			o, c = "<blockquote>", "</blockquote>"
		default:
			continue
		}
		start, end := max(0, e.Offset), min(len(u16), e.Offset+e.Length)
		if end > start {
			spans = append(spans, span{start, end, o, c})
		}
	}
	// Outer spans (longer) open first so they close last.
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	var out strings.Builder
	var stack []span
	pos, next := 0, 0
	for p := 0; p <= len(u16); p++ {
		endsHere := func() bool {
			for _, s := range stack {
				if s.end == p {
					return true
				}
			}
			return false
		}
		if endsHere() || (next < len(spans) && spans[next].start == p) {
			out.WriteString(tg.Esc(utf16Decode(u16[pos:p])))
			pos = p
		}
		// Close every span ending here; spans above them that continue are
		// closed and reopened so tags always nest properly.
		var reopen []span
		for endsHere() {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			out.WriteString(top.close)
			if top.end != p {
				reopen = append(reopen, top)
			}
		}
		for i := len(reopen) - 1; i >= 0; i-- {
			out.WriteString(reopen[i].open)
			stack = append(stack, reopen[i])
		}
		for next < len(spans) && spans[next].start == p {
			out.WriteString(spans[next].open)
			stack = append(stack, spans[next])
			next++
		}
	}
	out.WriteString(tg.Esc(utf16Decode(u16[pos:])))
	return out.String()
}

func utf16Encode(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

func utf16Decode(u []uint16) string {
	var b strings.Builder
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xD800 && c < 0xDC00 && i+1 < len(u) {
			c = 0x10000 + (c-0xD800)<<10 + (rune(u[i+1]) - 0xDC00)
			i++
		}
		b.WriteRune(c)
	}
	return b.String()
}

// sendBackup sends a consistent database snapshot to the owner.
func (b *Bot) sendBackup(ctx context.Context) {
	lang := b.lang
	dir, err := os.MkdirTemp("", "tadmin-backup-*")
	if err != nil {
		b.notifyOwner(ctx, t(lang, "backup_failed", tg.Esc(err.Error())), nil)
		return
	}
	defer os.RemoveAll(dir)
	name := "tadmin-" + time.Now().In(b.cfg.Location).Format("20060102-150405") + ".db"
	path := filepath.Join(dir, name)
	if err := b.st.Backup(ctx, path); err != nil {
		b.notifyOwner(ctx, t(lang, "backup_failed", tg.Esc(err.Error())), nil)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.notifyOwner(ctx, t(lang, "backup_failed", tg.Esc(err.Error())), nil)
		return
	}
	if len(data) > 49<<20 {
		b.notifyOwner(ctx, t(lang, "backup_too_big", humanBytes(int64(len(data)))), nil)
		return
	}
	if _, err := b.tg.SendDocument(ctx, tg.SendDocumentParams{ChatID: b.cfg.OwnerID, Caption: t(lang, "backup_caption", humanBytes(int64(len(data))))}, name, data); err != nil {
		b.notifyOwner(ctx, t(lang, "backup_failed", tg.Esc(err.Error())), nil)
	}
}

// urlCard offers quick actions for links the owner sends.
func (b *Bot) urlCard(ctx context.Context, urls []string) {
	lang := b.lang
	if len(urls) > 1 {
		var sb strings.Builder
		sb.WriteString(t(lang, "urls_received", len(urls)))
		for _, u := range urls {
			sb.WriteString("\n• " + tg.Esc(tg.Truncate(u, 80)))
		}
		id := b.remember(strings.Join(urls, "\n"))
		b.show(ctx, b.cfg.OwnerID, 0, screen{text: sb.String(), kb: tg.Keyboard(tg.Row(tg.CBStyle(t(lang, "btn_refresh_all"), "pv:ra:"+id, tg.StylePrimary)))})
		return
	}
	u := urls[0]
	id := b.remember(u)
	rows := [][]tg.Button{}
	if b.Preview.Enabled() {
		rows = append(rows, tg.Row(tg.CBStyle(t(lang, "btn_refresh_preview"), "pv:r:"+id, tg.StylePrimary)))
	}
	rows = append(rows, tg.Row(tg.CB(t(lang, "btn_add_source"), "pv:d:"+id)))
	text := t(lang, "url_card", tg.Esc(tg.Truncate(u, 200)))
	if !b.Preview.Enabled() {
		text += "\n\n" + t(lang, "pv_userbot_missing")
	}
	b.show(ctx, b.cfg.OwnerID, 0, screen{text: text, kb: &tg.InlineKeyboard{InlineKeyboard: rows}})
}

// refreshURLs refreshes several urls: the first immediately, the rest queued.
func (b *Bot) refreshURLs(ctx context.Context, urls []string) {
	lang := b.lang
	if !b.Preview.Enabled() {
		b.notifyOwner(ctx, t(lang, "pv_userbot_missing"), tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_userbot"), "p:ub"))))
		return
	}
	if len(urls) == 1 {
		b.refreshOne(ctx, 0, urls[0])
		return
	}
	n := 0
	for _, u := range urls {
		if _, err := b.Preview.Enqueue(ctx, u, true); err == nil {
			n++
		}
	}
	b.notifyOwner(ctx, t(lang, "pv_queued_n", n), tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_preview"), "p:pv"))))
}

// refreshOne runs a manual refresh and reports the result in place.
func (b *Bot) refreshOne(ctx context.Context, msgID int, u string) {
	lang := b.lang
	msgID = b.show(ctx, b.cfg.OwnerID, msgID, screen{text: t(lang, "pv_refreshing", tg.Esc(tg.Truncate(u, 200)))})
	rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	r, err := b.Preview.RefreshNow(rctx, u)
	var text string
	var fw *userbot.FloodWaitError
	switch {
	case errors.As(err, &fw):
		text = t(lang, "pv_flood", humanDuration(fw.Wait+time.Minute, lang))
	case errors.Is(err, userbot.ErrNotReady):
		text = t(lang, "pv_not_ready")
	case errors.Is(err, userbot.ErrTimeout):
		text = t(lang, "pv_timeout")
	case err != nil:
		text = t(lang, "pv_error", tg.Esc(err.Error()))
	default:
		text = t(lang, "pv_result_"+string(r.Outcome), fmt.Sprintf("%.1f", r.Duration.Seconds()))
		if r.Reply != "" {
			text += "\n\n<blockquote>" + tg.Esc(tg.Truncate(r.Reply, 300)) + "</blockquote>"
		}
	}
	id := b.remember(u)
	b.show(ctx, b.cfg.OwnerID, msgID, screen{
		text: "🔗 " + tg.Esc(tg.Truncate(u, 200)) + "\n\n" + text,
		kb:   tg.Keyboard(tg.Row(tg.CB(t(lang, "btn_retry"), "pv:r:"+id), tg.CB(t(lang, "btn_preview"), "p:pv"))),
	})
}

func isSecretSession(kind string) bool {
	return kind == "phone" || kind == "password" || kind == "code"
}
