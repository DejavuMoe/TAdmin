package bot

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"tadmin/internal/channel"
	"tadmin/internal/config"
	"tadmin/internal/gate"
	"tadmin/internal/preview"
	"tadmin/internal/preview/feeds"
	"tadmin/internal/roles"
	"tadmin/internal/store"
	"tadmin/internal/tg"
	"tadmin/internal/tgtest"
)

const (
	ownerID = int64(1)
	adminID = int64(2)
	userID  = int64(4242)
	groupID = int64(-100777)
)

type harness struct {
	t   *testing.T
	srv *tgtest.Server
	st  *store.Store
	b   *Bot
	ctx context.Context
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv := tgtest.New(t)
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg, err := config.Parse(nil, func(k string) (string, bool) {
		return map[string]string{"TADMIN_BOT_TOKEN": "123:TEST", "TADMIN_OWNER_ID": "1", "TADMIN_TIMEZONE": "UTC"}[k], k == "TADMIN_BOT_TOKEN" || k == "TADMIN_OWNER_ID" || k == "TADMIN_TIMEZONE"
	})
	if err != nil {
		t.Fatal(err)
	}
	client := srv.Client()
	rc := roles.New(client, ownerID, time.Minute)
	srv.SetAdmins(groupID, ownerID, adminID)
	log := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	eng := gate.New(gate.Config{Store: st, TG: client, Roles: rc, OwnerID: ownerID, BotID: srv.Bot.ID, BotUsername: srv.Bot.Username, Lang: "zh-CN", Logger: log})
	pv := preview.New(preview.Config{Store: st, Fetcher: feeds.NewFetcher(feeds.Options{}), CheckInterval: time.Hour, MinGap: time.Minute, DailyLimit: 10, Logger: log})
	me := srv.Bot
	b := New(Deps{Config: &cfg, Store: st, TG: client, Roles: rc, Gate: eng, Channel: channel.New(st, client, log), Preview: pv,
		Me: &me, Logger: log, Version: "test", Started: time.Now()})
	b.inline = true
	return &harness{t: t, srv: srv, st: st, b: b, ctx: ctx}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func (h *harness) update(u tg.Update) { h.b.handle(h.ctx, &u) }

func (h *harness) private(from int64, text string) {
	h.update(tg.Update{Message: &tg.Message{MessageID: 1, From: &tg.User{ID: from, FirstName: "U"}, Chat: tg.Chat{ID: from, Type: tg.ChatPrivate},
		Text: text, Entities: commandEntity(text)}})
}

func commandEntity(text string) []tg.MessageEntity {
	if !strings.HasPrefix(text, "/") {
		return nil
	}
	n := strings.IndexAny(text, " \n")
	if n < 0 {
		n = len(text)
	}
	return []tg.MessageEntity{{Type: "bot_command", Offset: 0, Length: n}}
}

func (h *harness) callback(from int64, chat int64, msgID int, data string) tgtest.Call {
	h.srv.Reset()
	typ := tg.ChatPrivate
	if chat < 0 {
		typ = tg.ChatSupergroup
	}
	h.update(tg.Update{CallbackQuery: &tg.CallbackQuery{ID: "cq", From: tg.User{ID: from, FirstName: "U"}, Data: data,
		Message: &tg.Message{MessageID: msgID, Chat: tg.Chat{ID: chat, Type: typ}}}})
	c, _ := h.srv.Last("answerCallbackQuery")
	return c
}

func (h *harness) addGroup(by int64) {
	h.update(tg.Update{MyChatMember: &tg.ChatMemberUpdated{
		Chat: tg.Chat{ID: groupID, Type: tg.ChatSupergroup, Title: "Demo Group"}, From: tg.User{ID: by, FirstName: "Adder"},
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: h.srv.Bot},
		NewChatMember: tg.ChatMember{Status: tg.StatusAdministrator, User: h.srv.Bot, CanRestrictMembers: true},
	}})
}

// lastText returns the text of the last sent or edited message.
func (h *harness) lastText() string {
	calls := h.srv.Calls("sendMessage", "editMessageText")
	if len(calls) == 0 {
		return ""
	}
	return calls[len(calls)-1].Str("text")
}

func TestOwnerAddsGroupGetsSetupCard(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	c, err := h.st.GetChat(h.ctx, groupID)
	if err != nil || c.Status != store.ChatActive {
		t.Fatalf("chat = %+v, %v", c, err)
	}
	card, ok := h.srv.Last("sendMessage")
	if !ok || card.Int("chat_id") != ownerID || !strings.Contains(card.Str("text"), "Demo Group") || !strings.Contains(card.Str("text"), "✅") {
		t.Fatalf("setup card: %+v", card.Params)
	}
}

func TestStrangerAddsGroupNeedsAuthorization(t *testing.T) {
	h := newHarness(t)
	h.addGroup(userID)
	c, _ := h.st.GetChat(h.ctx, groupID)
	if c.Status != store.ChatPending {
		t.Fatalf("status = %s", c.Status)
	}
	// Joins in a pending chat are ignored.
	u := tg.User{ID: 5, FirstName: "N"}
	h.update(tg.Update{ChatMember: &tg.ChatMemberUpdated{Chat: tg.Chat{ID: groupID}, From: u,
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u}, NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: u}}})
	if _, ok := h.srv.Last("restrictChatMember"); ok {
		t.Fatal("pending chat verified a member")
	}
	if cq := h.callback(userID, userID, 5, "au:ok:"+strconv.FormatInt(groupID, 10)); cq.Params["show_alert"] != true {
		t.Fatal("non-owner could authorize")
	}
	h.callback(ownerID, ownerID, 5, "au:ok:"+strconv.FormatInt(groupID, 10))
	if c, _ = h.st.GetChat(h.ctx, groupID); c.Status != store.ChatActive {
		t.Fatalf("status after authorize = %s", c.Status)
	}
}

func TestPanelNavigationAndToggles(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	h.private(ownerID, "/start")
	if !strings.Contains(h.lastText(), "控制台") {
		t.Fatalf("home: %s", h.lastText())
	}
	id := strconv.FormatInt(groupID, 10)
	pages := []string{"p:home", "p:groups", "p:stats", "p:g:" + id, "p:v:" + id, "p:w:" + id, "p:c:" + id, "p:cl:" + id,
		"p:sec:" + id, "p:q:" + id, "p:st:" + id, "p:chk:" + id, "p:pv", "p:pvl", "p:pvrec", "p:pvclr", "p:sys", "p:del:" + id, "p:call:" + id}
	for _, p := range pages {
		h.callback(ownerID, ownerID, 10, p)
		txt := h.lastText()
		if txt == "" {
			t.Errorf("%s rendered nothing", p)
		}
		if strings.Contains(txt, "%!") || strings.Contains(txt, "_short") {
			t.Errorf("%s has formatting problems: %s", p, txt)
		}
	}
	for _, f := range []string{"ven", "ven", "vdel", "vkind", "vto:1", "vto:-1", "vat:1", "vfail", "vrt", "vcd", "vreq", "wen", "weph", "wdel",
		"cun", "cmask", "kj", "kl", "kp", "ko", "kc", "rth", "rmin", "cas", "bots", "lang"} {
		parts := strings.SplitN(f, ":", 2)
		data := "s:" + id + ":" + parts[0]
		if len(parts) == 2 {
			data += ":" + parts[1]
		}
		if cq := h.callback(ownerID, ownerID, 10, data); cq.Params["show_alert"] == true {
			t.Errorf("toggle %s rejected: %v", f, cq.Params["text"])
		}
		if txt := h.lastText(); strings.Contains(txt, "%!") {
			t.Errorf("toggle %s: %s", f, txt)
		}
	}
	c, _ := h.st.GetChat(h.ctx, groupID)
	s := c.Settings
	if s.Verify.Delivery != store.DeliveryPublic || s.Verify.Kind != "math" || s.Verify.Timeout != 180 || s.Verify.Attempts != 4 ||
		s.Verify.OnFail != "review" || s.Welcome.Enabled != true || s.Channel.Unpin != "unpin" || s.Language != "en" {
		t.Fatalf("settings after toggles: %+v", s)
	}
}

func TestNonAdminCannotOpenGroupPanel(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	id := strconv.FormatInt(groupID, 10)
	if cq := h.callback(userID, userID, 10, "p:g:"+id); cq.Params["show_alert"] != true {
		t.Fatal("stranger opened the group panel")
	}
	if cq := h.callback(userID, userID, 10, "s:"+id+":ven"); cq.Params["show_alert"] != true {
		t.Fatal("stranger toggled a setting")
	}
	if cq := h.callback(adminID, adminID, 10, "p:g:"+id); cq.Params["show_alert"] == true {
		t.Fatal("group admin could not open the panel")
	}
	if cq := h.callback(adminID, adminID, 10, "p:sys"); cq.Params["show_alert"] != true {
		t.Fatal("group admin opened owner-only system page")
	}
}

func TestWelcomeTextInputKeepsFormatting(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	id := strconv.FormatInt(groupID, 10)
	h.callback(ownerID, ownerID, 10, "p:wt:"+id)
	h.update(tg.Update{Message: &tg.Message{MessageID: 2, From: &tg.User{ID: ownerID}, Chat: tg.Chat{ID: ownerID, Type: tg.ChatPrivate},
		Text: "Hi {mention} 👋 read rules", Entities: []tg.MessageEntity{{Type: "bold", Offset: 0, Length: 2}, {Type: "text_link", Offset: 21, Length: 5, URL: "https://t.me/x/1"}}}})
	c, _ := h.st.GetChat(h.ctx, groupID)
	if c.Settings.Welcome.Text != `<b>Hi</b> {mention} 👋 read <a href="https://t.me/x/1">rules</a>` || !c.Settings.Welcome.Enabled {
		t.Fatalf("welcome text = %q", c.Settings.Welcome.Text)
	}
}

func TestGroupCommands(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	send := func(from int64, text string, reply *tg.User) {
		m := &tg.Message{MessageID: 50, From: &tg.User{ID: from, FirstName: "A"}, Chat: tg.Chat{ID: groupID, Type: tg.ChatSupergroup},
			Text: text, Entities: commandEntity(text)}
		if reply != nil {
			m.ReplyToMessage = &tg.Message{MessageID: 49, From: reply, Chat: m.Chat}
		}
		h.srv.Reset()
		h.update(tg.Update{Message: m})
	}
	target := &tg.User{ID: userID, FirstName: "Spammer"}
	send(userID, "/ban", target)
	if _, ok := h.srv.Last("banChatMember"); ok {
		t.Fatal("non-admin command executed")
	}
	send(adminID, "/mute 2h", target)
	r, ok := h.srv.Last("restrictChatMember")
	if !ok || r.Int("user_id") != userID {
		t.Fatal("mute not executed")
	}
	send(adminID, "/ban 7d", target)
	if b, ok := h.srv.Last("banChatMember"); !ok || b.Int("until_date") == 0 {
		t.Fatal("timed ban not executed")
	}
	send(adminID, "/kick", &tg.User{ID: adminID, FirstName: "Admin"})
	if _, ok := h.srv.Last("banChatMember"); ok {
		t.Fatal("admin could be kicked")
	}
	send(adminID, "/unban "+strconv.FormatInt(userID, 10), nil)
	if u, ok := h.srv.Last("unbanChatMember"); !ok || u.Int("user_id") != userID {
		t.Fatal("unban by id failed")
	}
}

func TestServiceMessageCleanupAndGateEnforcement(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	u := tg.User{ID: userID, FirstName: "New"}
	h.srv.SetMember(groupID, tg.ChatMember{Status: tg.StatusMember, User: u})
	h.srv.Reset()
	h.update(tg.Update{Message: &tg.Message{MessageID: 70, From: &u, Chat: tg.Chat{ID: groupID, Type: tg.ChatSupergroup}, NewChatMembers: []tg.User{u}}})
	if d, ok := h.srv.Last("deleteMessage"); !ok || d.Int("message_id") != 70 {
		t.Fatal("join service message not deleted")
	}
	if _, err := h.st.OpenGateFor(h.ctx, groupID, userID); err != nil {
		t.Fatal("service message fallback did not start verification")
	}
	h.srv.Reset()
	h.update(tg.Update{Message: &tg.Message{MessageID: 71, From: &u, Chat: tg.Chat{ID: groupID, Type: tg.ChatSupergroup}, Text: "buy crypto"}})
	if d, ok := h.srv.Last("deleteMessage"); !ok || d.Int("message_id") != 71 {
		t.Fatal("message from unverified newcomer not removed")
	}
}

func TestChannelAutoUnpinAndCleanKeepsAdminPins(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	h.b.Channel.SetDelay(10 * time.Millisecond)
	if _, err := h.st.UpdateSettings(h.ctx, groupID, func(s *store.Settings) { s.Channel.Unpin = "unpin" }); err != nil {
		t.Fatal(err)
	}
	channelChat := &tg.Chat{ID: -100555, Type: tg.ChatChannel, Title: "News"}
	adminPin := &tg.Message{MessageID: 10, From: &tg.User{ID: adminID}, Chat: tg.Chat{ID: groupID}}
	h.srv.Pin(groupID, adminPin)
	old := &tg.Message{MessageID: 11, SenderChat: channelChat, IsAutomaticForward: true, Chat: tg.Chat{ID: groupID}}
	h.srv.Pin(groupID, old)
	fwd := &tg.Message{MessageID: 12, SenderChat: channelChat, IsAutomaticForward: true, Chat: tg.Chat{ID: groupID, Type: tg.ChatSupergroup}}
	h.srv.Pin(groupID, fwd)
	h.update(tg.Update{Message: fwd})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(h.srv.Pinned(groupID)) == 3 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := h.srv.Pinned(groupID); len(got) != 2 {
		t.Fatalf("auto-unpin: pinned = %v", got)
	}
	c, _ := h.st.GetChat(h.ctx, groupID)
	res, err := h.b.Channel.CleanPins(h.ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.srv.Pinned(groupID); len(got) != 1 || got[0] != 10 || res.StoppedAt != "admin" {
		t.Fatalf("clean: pinned = %v result %+v", got, res)
	}
}

func TestChannelMaskBlocked(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	_, _ = h.st.UpdateSettings(h.ctx, groupID, func(s *store.Settings) { s.Channel.BlockMasks = true })
	_ = h.st.SetChatInfo(h.ctx, groupID, "Demo Group", "", -100555)
	h.srv.Reset()
	// Linked channel and anonymous admin are fine.
	h.update(tg.Update{Message: &tg.Message{MessageID: 80, SenderChat: &tg.Chat{ID: -100555, Type: tg.ChatChannel}, Chat: tg.Chat{ID: groupID, Type: tg.ChatSupergroup}, Text: "hi"}})
	h.update(tg.Update{Message: &tg.Message{MessageID: 81, SenderChat: &tg.Chat{ID: groupID, Type: tg.ChatSupergroup}, Chat: tg.Chat{ID: groupID, Type: tg.ChatSupergroup}, Text: "hi"}})
	if _, ok := h.srv.Last("banChatSenderChat"); ok {
		t.Fatal("linked channel / anonymous admin blocked")
	}
	h.update(tg.Update{Message: &tg.Message{MessageID: 82, SenderChat: &tg.Chat{ID: -100999, Type: tg.ChatChannel, Title: "Ads"}, Chat: tg.Chat{ID: groupID, Type: tg.ChatSupergroup}, Text: "promo"}})
	if b, ok := h.srv.Last("banChatSenderChat"); !ok || b.Int("sender_chat_id") != -100999 {
		t.Fatal("foreign channel identity not blocked")
	}
}

func TestDemoChallenge(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	h.srv.Reset()
	h.callback(ownerID, ownerID, 10, "p:demo:"+strconv.FormatInt(groupID, 10))
	p, ok := h.srv.Last("sendPhoto")
	if !ok || !strings.Contains(p.Str("reply_markup"), "dm:") {
		t.Fatal("demo not sent")
	}
}

func TestEntitiesToHTML(t *testing.T) {
	got := entitiesToHTML("a<b> 😀 x", []tg.MessageEntity{{Type: "italic", Offset: 5, Length: 2}})
	if got != "a&lt;b&gt; <i>😀</i> x" {
		t.Fatalf("got %q", got)
	}
}

func TestSparklineAndDurations(t *testing.T) {
	if s := sparkline([]int{0, 1, 2, 8}); s != "▁▂▃█" {
		t.Fatalf("sparkline %q", s)
	}
	for in, want := range map[string]time.Duration{"10m": 10 * time.Minute, "2h": 2 * time.Hour, "7d": 7 * 24 * time.Hour, "30": 30 * time.Minute} {
		if d, ok := parseDuration(in); !ok || d != want {
			t.Errorf("parseDuration(%q) = %v", in, d)
		}
	}
	if _, ok := parseDuration("x"); ok {
		t.Error("parsed garbage")
	}
}

func TestEntitiesToHTMLNesting(t *testing.T) {
	cases := []struct {
		text string
		ents []tg.MessageEntity
		want string
	}{
		{"hello", []tg.MessageEntity{{Type: "bold", Offset: 0, Length: 5}, {Type: "italic", Offset: 0, Length: 5}}, "<b><i>hello</i></b>"},
		{"hello world", []tg.MessageEntity{{Type: "bold", Offset: 0, Length: 11}, {Type: "italic", Offset: 6, Length: 5}}, "<b>hello <i>world</i></b>"},
		{"abcdef", []tg.MessageEntity{{Type: "bold", Offset: 0, Length: 4}, {Type: "italic", Offset: 2, Length: 4}}, "<b>ab<i>cd</i></b><i>ef</i>"},
	}
	for _, c := range cases {
		if got := entitiesToHTML(c.text, c.ents); got != c.want {
			t.Errorf("entitiesToHTML(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestPendingChatCannotBeActivatedByStranger(t *testing.T) {
	h := newHarness(t)
	h.srv.SetAdmins(groupID, userID) // the stranger owns their own group
	h.addGroup(userID)
	id := strconv.FormatInt(groupID, 10)
	h.private(userID, "/start g"+id)
	if strings.Contains(h.lastText(), "Demo Group") && strings.Contains(h.lastText(), "<code>") {
		t.Fatal("stranger opened the panel of a pending group")
	}
	for _, data := range []string{"p:pause:" + id, "p:g:" + id, "s:" + id + ":ven", "rl:" + id} {
		if cq := h.callback(userID, userID, 10, data); cq.Params["show_alert"] != true {
			t.Errorf("%s allowed on a pending group", data)
		}
	}
	if c, _ := h.st.GetChat(h.ctx, groupID); c.Status != store.ChatPending {
		t.Fatalf("status changed to %s", c.Status)
	}
}

func TestPendingListNotLeakedToStrangers(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	u := tg.User{ID: 5, FirstName: "Secret Newcomer"}
	h.srv.SetMember(groupID, tg.ChatMember{Status: tg.StatusMember, User: u})
	h.update(tg.Update{ChatMember: &tg.ChatMemberUpdated{Chat: tg.Chat{ID: groupID}, From: u,
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u}, NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: u}}})
	g, err := h.st.OpenGateFor(h.ctx, groupID, 5)
	if err != nil {
		t.Fatal(err)
	}
	h.callback(userID, userID, 10, "q:"+strconv.FormatInt(g.ID, 10)+":a")
	for _, c := range h.srv.Calls("sendMessage", "editMessageText") {
		if strings.Contains(c.Str("text"), "Secret Newcomer") {
			t.Fatal("pending list leaked to a stranger")
		}
	}
}

func TestMalformedCallbacksDoNotPanic(t *testing.T) {
	h := newHarness(t)
	h.addGroup(ownerID)
	for _, data := range []string{"p", "p:", "p:v", "p:q:0", "p:g:abc", "ub", "kp", "s", "s:x", "q", "pv", "pv:r", "rl", "au", "g:", "g:x:y", "dm", "sys"} {
		for _, from := range []int64{ownerID, userID} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%q from %d panicked: %v", data, from, r)
					}
				}()
				h.update(tg.Update{CallbackQuery: &tg.CallbackQuery{ID: "x", From: tg.User{ID: from}, Data: data,
					Message: &tg.Message{MessageID: 1, Chat: tg.Chat{ID: from, Type: tg.ChatPrivate}}}})
			}()
		}
	}
}
