package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"tadmin/internal/captcha"
	"tadmin/internal/gate"
	"tadmin/internal/store"
	"tadmin/internal/tg"
)

var (
	timeoutSteps  = []int{60, 90, 120, 180, 300, 600}
	attemptSteps  = []int{1, 2, 3, 4, 5}
	deleteSteps   = []int{0, 60, 300, 1800, 3600}
	raidSteps     = []int{0, 5, 10, 20, 30, 50}
	raidMinSteps  = []int{5, 15, 30, 60}
	reviewSteps   = []int{300, 900, 1800, 3600}
	deliveryModes = []string{store.DeliveryOverlay, store.DeliveryPublic, store.DeliveryPrivate}
	kinds         = []string{"image", "math", "button"}
	unpinModes    = []string{"off", "unpin", "keep_latest"}
	aiModes       = []string{"off", "hint", "review"}
)

func (b *Bot) verifyScreen(ctx context.Context, c *store.Chat, lang string) screen {
	v := c.Settings.Verify
	id := itoa(c.ID)
	var sb strings.Builder
	sb.WriteString(t(lang, "verify_title", tg.Esc(c.Title)))
	sb.WriteString("\n\n" + t(lang, "verify_desc_"+v.Delivery))
	sb.WriteString("\n" + t(lang, "verify_kind_desc_"+v.Kind))
	if v.OnFail == "review" {
		sb.WriteString("\n" + t(lang, "verify_fail_review_desc", humanSeconds(v.ReviewTTL, lang)))
	} else {
		sb.WriteString("\n" + t(lang, "verify_fail_kick_desc"))
	}
	if v.Cooldown {
		sb.WriteString("\n" + t(lang, "verify_cooldown_desc"))
	}
	rows := [][]tg.Button{
		tg.Row(toggle(t(lang, "lbl_verify_enabled"), v.Enabled, "s:"+id+":ven")),
		tg.Row(tg.CB(t(lang, "lbl_delivery", b.deliveryName(lang, v.Delivery)), "s:"+id+":vdel"),
			tg.CB(t(lang, "lbl_kind", b.kindName(lang, v.Kind)), "s:"+id+":vkind")),
		tg.Row(tg.CB("⏱ −", "s:"+id+":vto:-1"), tg.CB(humanSeconds(v.Timeout, lang), "s:"+id+":vto:0"), tg.CB("+", "s:"+id+":vto:1")),
		tg.Row(tg.CB("🎯 −", "s:"+id+":vat:-1"), tg.CB(t(lang, "lbl_attempts", v.Attempts), "s:"+id+":vat:0"), tg.CB("+", "s:"+id+":vat:1")),
		tg.Row(tg.CB(t(lang, "lbl_onfail_"+v.OnFail), "s:"+id+":vfail"), toggle(t(lang, "lbl_cooldown"), v.Cooldown, "s:"+id+":vcd")),
	}
	if v.OnFail == "review" {
		rows = append(rows, tg.Row(tg.CB(t(lang, "lbl_review_ttl", humanSeconds(v.ReviewTTL, lang)), "s:"+id+":vrt")))
	}
	reqRow := tg.Row(toggle(t(lang, "lbl_requests"), v.Requests, "s:"+id+":vreq"))
	if b.WebReady {
		reqRow = append(reqRow, toggle(t(lang, "lbl_webapp"), v.WebApp, "s:"+id+":vweb"))
	}
	rows = append(rows, reqRow,
		tg.Row(tg.CBStyle(t(lang, "btn_demo"), "p:demo:"+id, tg.StylePrimary)),
		tg.Row(back(lang, "p:g:"+id)))
	return screen{text: sb.String(), kb: &tg.InlineKeyboard{InlineKeyboard: rows}}
}

func (b *Bot) welcomeScreen(ctx context.Context, c *store.Chat, lang, note string) screen {
	w := c.Settings.Welcome
	id := itoa(c.ID)
	var sb strings.Builder
	sb.WriteString(t(lang, "welcome_title", tg.Esc(c.Title)))
	tpl := w.Text
	if strings.TrimSpace(tpl) == "" {
		tpl = t(c.Lang(b.lang), "welcome_default")
		sb.WriteString("\n\n" + t(lang, "welcome_using_default"))
	}
	sb.WriteString("\n\n<blockquote>" + tpl + "</blockquote>")
	if w.Buttons != "" {
		sb.WriteString("\n" + t(lang, "welcome_buttons_set", strings.Count(strings.TrimSpace(w.Buttons), "\n")+1))
	}
	sb.WriteString("\n\n" + t(lang, "welcome_placeholders"))
	if note != "" {
		sb.WriteString("\n\n" + note)
	}
	del := t(lang, "lbl_never")
	if w.DeleteAfter > 0 {
		del = humanSeconds(w.DeleteAfter, lang)
	}
	vis := t(lang, "lbl_vis_public")
	if w.Ephemeral {
		vis = t(lang, "lbl_vis_ephemeral")
	}
	kb := tg.Keyboard(
		tg.Row(toggle(t(lang, "lbl_welcome_enabled"), w.Enabled, "s:"+id+":wen")),
		tg.Row(tg.CB(vis, "s:"+id+":weph"), tg.CB(t(lang, "lbl_autodelete", del), "s:"+id+":wdel")),
		tg.Row(tg.CB(t(lang, "btn_edit_text"), "p:wt:"+id), tg.CB(t(lang, "btn_edit_buttons"), "p:wb:"+id)),
		tg.Row(tg.CB(t(lang, "btn_preview_welcome"), "p:wp:"+id)),
		tg.Row(back(lang, "p:g:"+id)),
	)
	return screen{text: sb.String(), kb: kb}
}

func (b *Bot) previewWelcome(ctx context.Context, c *store.Chat, user *tg.User, lang string) {
	text, kb := gate.RenderWelcome(c.Settings.Welcome, c.Lang(b.lang), user.ID, user.FullName(), user.Username, c.Title)
	if _, err := b.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: user.ID, Text: text, ReplyMarkup: kb}); err != nil {
		_, _ = b.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: user.ID, Text: t(lang, "welcome_invalid", tg.Esc(err.Error()))})
	}
}

func (b *Bot) channelScreen(ctx context.Context, c *store.Chat, lang, note string) screen {
	ch := c.Settings.Channel
	id := itoa(c.ID)
	var sb strings.Builder
	sb.WriteString(t(lang, "channel_title", tg.Esc(c.Title)))
	if c.LinkedChatID != 0 {
		sb.WriteString("\n\n" + t(lang, "channel_linked", c.LinkedChatID))
	} else {
		sb.WriteString("\n\n" + t(lang, "channel_none"))
	}
	sb.WriteString("\n\n" + t(lang, "unpin_desc_"+ch.Unpin))
	sb.WriteString("\n" + t(lang, "mask_desc"))
	if note != "" {
		sb.WriteString("\n\n" + note)
	}
	kb := tg.Keyboard(
		tg.Row(tg.CB(t(lang, "lbl_unpin", t(lang, "unpin_"+ch.Unpin)), "s:"+id+":cun")),
		tg.Row(tg.CBStyle(t(lang, "btn_clean_pins"), "p:cclean:"+id, tg.StylePrimary)),
		tg.Row(tg.CB(t(lang, "btn_unpin_all"), "p:call:"+id)),
		tg.Row(toggle(t(lang, "lbl_block_masks"), ch.BlockMasks, "s:"+id+":cmask")),
		tg.Row(back(lang, "p:g:"+id)),
	)
	return screen{text: sb.String(), kb: kb}
}

func (b *Bot) cleanScreen(ctx context.Context, c *store.Chat, lang string) screen {
	cl := c.Settings.Clean
	id := itoa(c.ID)
	kb := tg.Keyboard(
		tg.Row(toggle(t(lang, "lbl_clean_join"), cl.Join, "s:"+id+":kj"), toggle(t(lang, "lbl_clean_leave"), cl.Leave, "s:"+id+":kl")),
		tg.Row(toggle(t(lang, "lbl_clean_pin"), cl.Pin, "s:"+id+":kp"), toggle(t(lang, "lbl_clean_other"), cl.Other, "s:"+id+":ko")),
		tg.Row(toggle(t(lang, "lbl_clean_commands"), cl.Commands, "s:"+id+":kc")),
		tg.Row(back(lang, "p:g:"+id)),
	)
	return screen{text: t(lang, "clean_title", tg.Esc(c.Title)) + "\n\n" + t(lang, "clean_desc"), kb: kb}
}

func (b *Bot) securityScreen(ctx context.Context, c *store.Chat, lang string) screen {
	s := c.Settings
	if b.cfg.TypeSafeProvider == "laya" && s.Verify.AI == "review" {
		s.Verify.AI = "hint"
	}
	id := itoa(c.ID)
	var sb strings.Builder
	sb.WriteString(t(lang, "security_title", tg.Esc(c.Title)))
	sb.WriteString("\n\n" + t(lang, "raid_desc"))
	sb.WriteString("\n" + t(lang, "cas_desc"))
	sb.WriteString("\n" + t(lang, "bots_desc"))
	if b.AIReady {
		sb.WriteString("\n" + t(lang, "ai_desc_"+s.Verify.AI))
	} else {
		sb.WriteString("\n" + t(lang, "ai_unavailable"))
	}
	raid := t(lang, "lbl_off")
	if s.Raid.Threshold > 0 {
		raid = t(lang, "lbl_raid_n", s.Raid.Threshold)
	}
	rows := [][]tg.Button{
		tg.Row(tg.CB(t(lang, "lbl_raid", raid), "s:"+id+":rth"), tg.CB(t(lang, "lbl_raid_minutes", s.Raid.Minutes), "s:"+id+":rmin")),
		tg.Row(toggle(t(lang, "lbl_cas"), s.Verify.CAS, "s:"+id+":cas"), toggle(t(lang, "lbl_kick_bots"), s.KickBots, "s:"+id+":bots")),
	}
	if b.AIReady {
		rows = append(rows, tg.Row(tg.CB(t(lang, "lbl_ai", t(lang, "ai_"+s.Verify.AI)), "s:"+id+":ai")))
	}
	if c.InLockdown(time.Now()) {
		rows = append(rows, tg.Row(tg.CBStyle(t(lang, "raid_lift"), "rl:"+id, tg.StyleSuccess)))
	}
	rows = append(rows, tg.Row(tg.CB(t(lang, "lbl_language", t(lang, "lang_"+c.Lang(b.lang))), "s:"+id+":lang")), tg.Row(back(lang, "p:g:"+id)))
	return screen{text: sb.String(), kb: &tg.InlineKeyboard{InlineKeyboard: rows}}
}

// onSetting applies one settings change and re-renders the page.
func (b *Bot) onSetting(ctx context.Context, cq *tg.CallbackQuery, chatID int64, field, value string, msgID int) {
	lang := b.userLang(&cq.From)
	if _, ok := b.chatFor(ctx, chatID, cq.From.ID); !ok {
		b.answer(ctx, cq, t(lang, "cb_admin_only_panel"), true)
		return
	}
	dir, _ := strconv.Atoi(value)
	if field == "cun" && !b.canPin(ctx, chatID, cq.From.ID) {
		b.answer(ctx, cq, t(lang, "need_pin_right"), true)
		return
	}
	if field == "ai" && !b.AIReady {
		b.answer(ctx, cq, t(lang, "ai_unavailable"), true)
		return
	}
	c, err := b.st.UpdateSettings(ctx, chatID, func(s *store.Settings) {
		v := &s.Verify
		switch field {
		case "ven":
			v.Enabled = !v.Enabled
		case "vdel":
			v.Delivery = cycle(deliveryModes, v.Delivery)
		case "vkind":
			v.Kind = cycle(kinds, v.Kind)
		case "vto":
			if dir == 0 {
				v.Timeout = 180
			} else {
				v.Timeout = stepIn(timeoutSteps, v.Timeout, dir)
			}
		case "vat":
			if dir == 0 {
				v.Attempts = 3
			} else {
				v.Attempts = stepIn(attemptSteps, v.Attempts, dir)
			}
		case "vfail":
			v.OnFail = cycle([]string{"kick", "review"}, v.OnFail)
		case "vrt":
			v.ReviewTTL = cycle(reviewSteps, v.ReviewTTL)
		case "vcd":
			v.Cooldown = !v.Cooldown
		case "vreq":
			v.Requests = !v.Requests
		case "vweb":
			v.WebApp = !v.WebApp
		case "cas":
			v.CAS = !v.CAS
		case "ai":
			if b.cfg.TypeSafeProvider == "laya" {
				v.AI = cycle([]string{"off", "hint"}, v.AI)
			} else {
				v.AI = cycle(aiModes, v.AI)
			}
		case "wen":
			s.Welcome.Enabled = !s.Welcome.Enabled
		case "weph":
			s.Welcome.Ephemeral = !s.Welcome.Ephemeral
		case "wdel":
			s.Welcome.DeleteAfter = cycle(deleteSteps, s.Welcome.DeleteAfter)
		case "cun":
			s.Channel.Unpin = cycle(unpinModes, s.Channel.Unpin)
		case "cmask":
			s.Channel.BlockMasks = !s.Channel.BlockMasks
		case "kj":
			s.Clean.Join = !s.Clean.Join
		case "kl":
			s.Clean.Leave = !s.Clean.Leave
		case "kp":
			s.Clean.Pin = !s.Clean.Pin
		case "ko":
			s.Clean.Other = !s.Clean.Other
		case "kc":
			s.Clean.Commands = !s.Clean.Commands
		case "rth":
			s.Raid.Threshold = cycle(raidSteps, s.Raid.Threshold)
		case "rmin":
			s.Raid.Minutes = cycle(raidMinSteps, s.Raid.Minutes)
		case "bots":
			s.KickBots = !s.KickBots
		case "lang":
			s.Language = cycle([]string{"zh-CN", "en"}, c0(s.Language, b.lang))
		}
	})
	if err != nil {
		b.answer(ctx, cq, t(lang, "error_generic"), true)
		return
	}
	b.answer(ctx, cq, t(lang, "saved_short"), false)
	var s screen
	switch {
	case strings.HasPrefix(field, "v"):
		s = b.verifyScreen(ctx, c, lang)
	case strings.HasPrefix(field, "w"):
		s = b.welcomeScreen(ctx, c, lang, "")
	case strings.HasPrefix(field, "c") && field != "cas":
		s = b.channelScreen(ctx, c, lang, "")
	case strings.HasPrefix(field, "k"):
		s = b.cleanScreen(ctx, c, lang)
	default:
		s = b.securityScreen(ctx, c, lang)
	}
	b.show(ctx, cq.From.ID, msgID, s)
}

func c0(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// demo is an in-memory practice challenge so admins can try each kind.
type demo struct {
	chatTitle string
	kind      captcha.Kind
	seed      []byte
	gen       int
	picks     []int
	lang      string
	started   time.Time
}

var demos = struct {
	sync.Mutex
	m map[string]*demo
}{m: map[string]*demo{}}

func (b *Bot) sendDemo(ctx context.Context, user *tg.User, c *store.Chat, lang string) {
	kind, _ := captcha.ParseKind(c.Settings.Verify.Kind)
	b.newDemo(ctx, user.ID, c.Title, kind, lang)
}

func (b *Bot) newDemo(ctx context.Context, userID int64, title string, kind captcha.Kind, lang string) {
	tok := make([]byte, 4)
	_, _ = rand.Read(tok)
	seed := make([]byte, 16)
	_, _ = rand.Read(seed)
	d := &demo{chatTitle: title, kind: kind, seed: seed, gen: 1, lang: lang, started: time.Now()}
	key := hex.EncodeToString(tok)
	demos.Lock()
	if len(demos.m) > 200 {
		demos.m = map[string]*demo{}
	}
	demos.m[key] = d
	demos.Unlock()
	ch, caption, kb, err := b.demoView(key, d, "")
	if err != nil {
		return
	}
	if ch.Image != nil {
		_, _ = b.tg.SendPhoto(ctx, tg.SendPhotoParams{ChatID: userID, Caption: caption, ReplyMarkup: kb}, ch.Image)
	} else {
		_, _ = b.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: userID, Text: caption, ReplyMarkup: kb})
	}
}

func (b *Bot) demoView(key string, d *demo, note string) (*captcha.Challenge, string, *tg.InlineKeyboard, error) {
	ch, err := captcha.Generate(d.kind, d.seed, d.gen, d.lang)
	if err != nil {
		return nil, "", nil, err
	}
	caption := t(d.lang, "demo_head", tg.Esc(d.chatTitle), b.kindName(d.lang, string(d.kind))) + "\n\n<b>" + tg.Esc(ch.Prompt) + "</b>"
	if note != "" {
		caption += "\n\n" + note
	}
	var rows [][]tg.Button
	if ch.Kind == captcha.KindImage {
		for r := 0; r < captcha.GridSize; r++ {
			var row []tg.Button
			for col := 0; col < captcha.GridSize; col++ {
				cell := r*captcha.GridSize + col
				label := "·"
				if i := slices.Index(d.picks, cell); i >= 0 {
					label = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣"}[min(i, 3)]
				}
				row = append(row, tg.CB(label, "dm:"+key+":p:"+strconv.Itoa(cell)))
			}
			rows = append(rows, row)
		}
	} else {
		var row []tg.Button
		for i, o := range ch.Options {
			row = append(row, tg.CB(o, "dm:"+key+":o:"+strconv.Itoa(i)))
			if len(row) == 3 {
				rows, row = append(rows, row), nil
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	var kindRow []tg.Button
	for _, k := range kinds {
		label := b.kindName(d.lang, k)
		if k == string(d.kind) {
			label = "• " + label
		}
		kindRow = append(kindRow, tg.CB(label, "dm:"+key+":k:"+k))
	}
	rows = append(rows, kindRow)
	return ch, caption, &tg.InlineKeyboard{InlineKeyboard: rows}, nil
}

func (b *Bot) onDemo(ctx context.Context, cq *tg.CallbackQuery) {
	parts := strings.Split(cq.Data, ":")
	if len(parts) < 3 || cq.Message == nil {
		b.answer(ctx, cq, "", false)
		return
	}
	demos.Lock()
	d := demos.m[parts[1]]
	demos.Unlock()
	lang := b.userLang(&cq.From)
	if d == nil {
		b.answer(ctx, cq, t(lang, "demo_expired"), false)
		return
	}
	chatID, msgID := cq.Message.Chat.ID, cq.Message.MessageID
	redraw := func(newImage bool, note string) {
		ch, caption, kb, err := b.demoView(parts[1], d, note)
		if err != nil {
			return
		}
		switch {
		case newImage && ch.Image != nil && len(cq.Message.Photo) > 0:
			_ = b.tg.EditMessagePhoto(ctx, chatID, msgID, ch.Image, caption, kb)
		case newImage && ch.Image != nil:
			_ = b.tg.DeleteMessage(ctx, chatID, msgID)
			_, _ = b.tg.SendPhoto(ctx, tg.SendPhotoParams{ChatID: chatID, Caption: caption, ReplyMarkup: kb}, ch.Image)
		case ch.Image == nil && len(cq.Message.Photo) > 0:
			_ = b.tg.DeleteMessage(ctx, chatID, msgID)
			_, _ = b.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: chatID, Text: caption, ReplyMarkup: kb})
		case ch.Image == nil:
			_ = b.tg.EditMessageText(ctx, tg.EditMessageTextParams{ChatID: chatID, MessageID: msgID, Text: caption, ReplyMarkup: kb})
		default:
			_ = b.tg.EditMessageReplyMarkup(ctx, chatID, msgID, kb)
		}
	}
	arg := ""
	if len(parts) > 3 {
		arg = parts[3]
	}
	switch parts[2] {
	case "k":
		if k, ok := captcha.ParseKind(arg); ok {
			d.kind, d.gen, d.picks, d.started = k, d.gen+1, nil, time.Now()
		}
		b.answer(ctx, cq, "", false)
		redraw(true, "")
		return
	case "p", "o":
		n, _ := strconv.Atoi(arg)
		var answer []int
		ch, err := captcha.Generate(d.kind, d.seed, d.gen, d.lang)
		if err != nil {
			return
		}
		if parts[2] == "p" {
			if i := slices.Index(d.picks, n); i >= 0 {
				d.picks = slices.Delete(d.picks, i, i+1)
			} else {
				d.picks = append(d.picks, n)
			}
			if len(d.picks) < ch.Picks {
				b.answer(ctx, cq, "", false)
				redraw(false, "")
				return
			}
			answer = d.picks
		} else {
			answer = []int{n}
		}
		ok := ch.Check(answer)
		elapsed := time.Since(d.started).Seconds()
		d.gen, d.picks, d.started = d.gen+1, nil, time.Now()
		if ok {
			b.answer(ctx, cq, t(lang, "demo_ok_short"), false)
			redraw(true, t(d.lang, "demo_ok", fmt.Sprintf("%.1f", elapsed)))
		} else {
			b.answer(ctx, cq, t(lang, "demo_wrong_short"), false)
			redraw(true, t(d.lang, "demo_wrong"))
		}
	default:
		b.answer(ctx, cq, "", false)
	}
}
