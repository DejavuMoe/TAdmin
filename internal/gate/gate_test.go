package gate

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"tadmin/internal/captcha"
	"tadmin/internal/roles"
	"tadmin/internal/store"
	"tadmin/internal/tg"
	"tadmin/internal/tgtest"
)

const (
	chatID  = int64(-100123)
	ownerID = int64(1)
	adminID = int64(2)
	userID  = int64(4242)
)

type env struct {
	t    *testing.T
	srv  *tgtest.Server
	st   *store.Store
	eng  *Engine
	mu   sync.Mutex
	now  time.Time
	ctx  context.Context
	chat *store.Chat
}

func newEnv(t *testing.T, mutate func(*store.Settings)) *env {
	t.Helper()
	e := &env{t: t, srv: tgtest.New(t), now: time.Unix(1_800_000_000, 0), ctx: context.Background()}
	st, err := store.OpenMemory(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.SetClock(e.clock)
	e.st = st
	client := e.srv.Client()
	e.srv.SetAdmins(chatID, ownerID, adminID)
	e.eng = New(Config{
		Store: st, TG: client, Roles: roles.New(client, ownerID, time.Minute), OwnerID: ownerID,
		BotID: e.srv.Bot.ID, BotUsername: e.srv.Bot.Username, Lang: "zh-CN", Now: e.clock,
	})
	if err := st.UpsertChat(e.ctx, &store.Chat{ID: chatID, Title: "Test Group", Status: store.ChatActive}); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		if _, err := st.UpdateSettings(e.ctx, chatID, mutate); err != nil {
			t.Fatal(err)
		}
	}
	e.reloadChat()
	return e
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func (e *env) reloadChat() {
	c, err := e.st.GetChat(e.ctx, chatID)
	if err != nil {
		e.t.Fatal(err)
	}
	e.chat = c
}

func user(id int64) tg.User {
	return tg.User{ID: id, FirstName: "User" + strconv.FormatInt(id, 10), LanguageCode: "zh-hans"}
}

// join simulates a direct join (member present on Telegram's side).
func (e *env) join(id int64, actor int64) {
	u := user(id)
	e.srv.SetMember(chatID, tg.ChatMember{Status: tg.StatusMember, User: u})
	e.reloadChat()
	from := u
	if actor != 0 {
		from = user(actor)
	}
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{
		Chat: tg.Chat{ID: chatID, Type: tg.ChatSupergroup}, From: from,
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u},
		NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: u},
	})
	e.tick()
}

// tick runs one scheduler pass over due gates.
func (e *env) tick() {
	ids, _ := e.st.DueGates(e.ctx, e.clock(), 100)
	for _, id := range ids {
		e.eng.Process(e.ctx, id)
	}
}

func (e *env) gate(id int64) *store.Gate {
	e.t.Helper()
	g, err := e.st.OpenGateFor(e.ctx, chatID, id)
	if err != nil {
		g, err = e.st.LastClosedGate(e.ctx, chatID, id)
	}
	if err != nil {
		e.t.Fatalf("no gate for %d: %v", id, err)
	}
	return g
}

func (e *env) press(from int64, data string, msg *tg.Message) *tgtest.Call {
	e.srv.Reset()
	e.eng.OnCallback(e.ctx, &tg.CallbackQuery{ID: "cq", From: user(from), Message: msg, Data: data})
	if c, ok := e.srv.Last("answerCallbackQuery"); ok {
		return &c
	}
	return nil
}

func ephMsg(g *store.Gate) *tg.Message {
	return &tg.Message{Chat: tg.Chat{ID: chatID, Type: tg.ChatSupergroup}, EphemeralMessageID: g.EphMsg}
}

// solve brute-forces the correct answer for a gate's current challenge.
func solve(t *testing.T, g *store.Gate) []int {
	t.Helper()
	kind, _ := captcha.ParseKind(g.Kind)
	ch, err := captcha.Generate(kind, g.Seed, g.Gen, g.Lang)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Kind != captcha.KindImage {
		for i := range ch.Options {
			if ch.Check([]int{i}) {
				return []int{i}
			}
		}
		t.Fatal("no option solves the challenge")
	}
	for a := 0; a < 9; a++ {
		for b := 0; b < 9; b++ {
			for c := 0; c < 9; c++ {
				if a != b && b != c && a != c && ch.Check([]int{a, b, c}) {
					return []int{a, b, c}
				}
			}
		}
	}
	t.Fatal("unsolvable challenge")
	return nil
}

func wrong(t *testing.T, g *store.Gate) []int {
	ans := solve(t, g)
	if len(ans) == 1 {
		return []int{(ans[0] + 1) % 6}
	}
	return []int{ans[1], ans[0], ans[2]}
}

// answerViaButtons taps the answer on a view.
func (e *env) answerViaButtons(g *store.Gate, ans []int, msg *tg.Message) {
	for _, cell := range ans {
		action := "p"
		if g.Kind != "image" {
			action = "o"
		}
		e.press(g.UserID, "g:"+strconv.FormatInt(g.ID, 10)+":"+action+":"+strconv.Itoa(g.Gen)+":"+strconv.Itoa(cell), msg)
	}
}

func TestOverlayPassFlow(t *testing.T) {
	e := newEnv(t, nil)
	e.join(userID, 0)
	g := e.gate(userID)
	if g.State != store.StateChallenge || g.Delivery != store.DeliveryOverlay {
		t.Fatalf("gate state = %s delivery %s", g.State, g.Delivery)
	}
	r, ok := e.srv.Last("restrictChatMember")
	if !ok || r.Obj("permissions")["can_send_messages"] != false {
		t.Fatalf("newcomer not muted: %+v", r)
	}
	if g.PromptMsg == 0 || g.EphMsg == 0 {
		t.Fatalf("expected public prompt and ephemeral challenge, got prompt=%d eph=%d", g.PromptMsg, g.EphMsg)
	}
	photo, _ := e.srv.Last("sendPhoto")
	if photo.Obj("ephemeral_message_parameters")["receiver_user_id"] != float64(userID) {
		t.Fatalf("challenge photo not ephemeral: %+v", photo.Params)
	}
	// Someone else cannot answer.
	if cq := e.press(777, "g:"+strconv.FormatInt(g.ID, 10)+":p:1:0", ephMsg(g)); cq == nil || cq.Params["show_alert"] != true {
		t.Fatalf("stranger press not rejected: %+v", cq)
	}
	ans := solve(t, g)
	e.answerViaButtons(g, ans, ephMsg(g))
	g = e.gate(userID)
	if g.State != store.StateClosed || g.Outcome != store.OutcomePass {
		t.Fatalf("gate = %s/%s, want closed/pass (err %s)", g.State, g.Outcome, g.LastError)
	}
	if m := e.srv.Member(chatID, userID); m.Status != tg.StatusMember {
		t.Fatalf("member not restored: %+v", m)
	}
	if del, ok := e.srv.Last("deleteEphemeralMessage"); !ok || del.Int("ephemeral_message_id") != int64(g.EphMsg) {
		t.Fatalf("ephemeral challenge not deleted")
	}
	found := false
	for _, c := range e.srv.Calls("deleteMessage") {
		if c.Int("message_id") == int64(g.PromptMsg) {
			found = true
		}
	}
	if !found {
		t.Fatal("public prompt not deleted")
	}
	n, _ := e.st.CountEvents(e.ctx, chatID, store.EvPass, e.now.Add(-time.Hour))
	if n != 1 {
		t.Fatalf("pass events = %d", n)
	}
}

func TestPickToggleAndReset(t *testing.T) {
	e := newEnv(t, nil)
	e.join(userID, 0)
	g := e.gate(userID)
	id := strconv.FormatInt(g.ID, 10)
	e.press(userID, "g:"+id+":p:1:4", ephMsg(g))
	e.press(userID, "g:"+id+":p:1:4", ephMsg(g)) // tap again: undo
	if g = e.gate(userID); g.Picks != "" {
		t.Fatalf("picks after undo = %q", g.Picks)
	}
	e.press(userID, "g:"+id+":p:1:2", ephMsg(g))
	if kb, ok := e.srv.Last("editEphemeralMessageReplyMarkup"); !ok || !strings.Contains(kb.Str("reply_markup"), "1️⃣") {
		t.Fatal("pick not rendered")
	}
	e.press(userID, "g:"+id+":r:1", ephMsg(g))
	if g = e.gate(userID); g.Picks != "" {
		t.Fatalf("picks after reset = %q", g.Picks)
	}
	// A stale generation is re-rendered, not counted.
	e.press(userID, "g:"+id+":p:9:1", ephMsg(g))
	if g = e.gate(userID); g.Attempts != 0 || g.Picks != "" {
		t.Fatalf("stale press counted: %+v", g)
	}
}

func TestWrongAnswersRemoveWithEscalatingCooldown(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Kind = "math" })
	for round, want := range []time.Duration{2 * time.Minute, 8 * time.Minute} {
		e.join(userID, 0)
		g := e.gate(userID)
		for i := 0; i < 3; i++ {
			g = e.gate(userID)
			if g.State != store.StateChallenge {
				break
			}
			e.answerViaButtons(g, wrong(t, g), ephMsg(g))
		}
		g = e.gate(userID)
		if g.State != store.StateClosed || g.Outcome != store.OutcomeFail || g.Attempts != 3 {
			t.Fatalf("round %d: gate = %s/%s attempts %d", round, g.State, g.Outcome, g.Attempts)
		}
		ban, ok := e.srv.Last("banChatMember")
		if !ok {
			t.Fatalf("round %d: not removed", round)
		}
		if got := time.Unix(ban.Int("until_date"), 0).Sub(e.now); got != want {
			t.Fatalf("round %d: ban %v, want %v", round, got, want)
		}
		e.advance(10 * time.Minute)
	}
}

func TestCooldownSchedule(t *testing.T) {
	want := []time.Duration{2 * time.Minute, 8 * time.Minute, 32 * time.Minute, 128 * time.Minute, 512 * time.Minute, 24 * time.Hour, 24 * time.Hour}
	for i, w := range want {
		if got := Cooldown(i + 1); got != w {
			t.Errorf("Cooldown(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestTimeoutRemoves(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Timeout = 60 })
	e.join(userID, 0)
	g := e.gate(userID)
	e.advance(59 * time.Second)
	e.eng.Process(e.ctx, g.ID)
	if g = e.gate(userID); g.State != store.StateChallenge {
		t.Fatalf("resolved early: %s", g.State)
	}
	e.advance(2 * time.Second)
	e.eng.Process(e.ctx, g.ID)
	if g = e.gate(userID); g.Outcome != store.OutcomeTimeout {
		t.Fatalf("outcome = %s", g.Outcome)
	}
	if m := e.srv.Member(chatID, userID); m.Status != tg.StatusKicked {
		t.Fatalf("member = %s", m.Status)
	}
}

func TestHelpGoesToReviewAndAdminApproves(t *testing.T) {
	e := newEnv(t, nil)
	e.join(userID, 0)
	g := e.gate(userID)
	id := strconv.FormatInt(g.ID, 10)
	e.press(userID, "g:"+id+":h", ephMsg(g))
	g = e.gate(userID)
	if g.State != store.StateReview || g.ReviewMsg != g.PromptMsg {
		t.Fatalf("state %s review msg %d prompt %d", g.State, g.ReviewMsg, g.PromptMsg)
	}
	if ed, ok := e.srv.Last("editMessageText"); !ok || !strings.Contains(ed.Str("text"), "待审核") {
		t.Fatal("prompt not turned into a review card")
	}
	// The mute is extended over the review window.
	if r, _ := e.srv.Last("restrictChatMember"); time.Unix(r.Int("until_date"), 0).Before(e.now.Add(15 * time.Minute)) {
		t.Fatal("mute not extended for review")
	}
	prompt := &tg.Message{Chat: tg.Chat{ID: chatID, Type: tg.ChatSupergroup}, MessageID: g.PromptMsg}
	if cq := e.press(userID, "g:"+id+":a", prompt); cq == nil || cq.Params["show_alert"] != true {
		t.Fatal("non-admin could approve")
	}
	e.press(adminID, "g:"+id+":a", prompt)
	g = e.gate(userID)
	if g.Outcome != store.OutcomeApprove || g.ActorID != adminID {
		t.Fatalf("outcome %s actor %d", g.Outcome, g.ActorID)
	}
	if m := e.srv.Member(chatID, userID); m.Status != tg.StatusMember {
		t.Fatalf("member = %s", m.Status)
	}
}

func TestReviewTimeoutRemoves(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) {
		s.Verify.OnFail = "review"
		s.Verify.ReviewTTL = 300
		s.Verify.Kind = "button"
	})
	e.join(userID, 0)
	g := e.gate(userID)
	e.eng.toReview(e.ctx, e.chat, g, "failed")
	e.advance(299 * time.Second)
	e.eng.Process(e.ctx, g.ID)
	if g = e.gate(userID); g.State != store.StateReview {
		t.Fatalf("review ended early: %s", g.State)
	}
	e.advance(2 * time.Second)
	e.eng.Process(e.ctx, g.ID)
	if g = e.gate(userID); g.Outcome != store.OutcomeTimeout {
		t.Fatalf("outcome = %s", g.Outcome)
	}
}

func TestPreservesEarlierAdminRestriction(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Kind = "button" })
	u := user(userID)
	until := e.now.Add(48 * time.Hour).Unix()
	e.srv.SetMember(chatID, tg.ChatMember{Status: tg.StatusRestricted, IsMember: true, User: u, UntilDate: until,
		ChatPermissions: tg.ChatPermissions{CanSendMessages: tg.Bool(true), CanSendPhotos: tg.Bool(false)}})
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: u,
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u},
		NewChatMember: tg.ChatMember{Status: tg.StatusRestricted, IsMember: true, User: u}})
	e.tick()
	g := e.gate(userID)
	e.answerViaButtons(g, solve(t, g), ephMsg(g))
	r, _ := e.srv.Last("restrictChatMember")
	p := r.Obj("permissions")
	if p["can_send_photos"] != false || p["can_send_messages"] != true || r.Int("until_date") != until {
		t.Fatalf("earlier restriction not restored: %+v until %d", p, r.Int("until_date"))
	}
}

func TestJoinRequestFlow(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Kind = "button" })
	u := user(userID)
	e.eng.OnJoinRequest(e.ctx, e.chat, &tg.ChatJoinRequest{Chat: tg.Chat{ID: chatID}, From: u, UserChatID: userID, Date: e.now.Unix()})
	e.tick()
	g := e.gate(userID)
	if g.Source != store.SourceRequest || g.CaptchaChat != userID || g.State != store.StateChallenge {
		t.Fatalf("request gate: %+v", g)
	}
	if _, ok := e.srv.Last("restrictChatMember"); ok {
		t.Fatal("join requests must not restrict")
	}
	msg := &tg.Message{Chat: tg.Chat{ID: userID, Type: tg.ChatPrivate}, MessageID: g.CaptchaMsg}
	e.answerViaButtons(g, solve(t, g), msg)
	if _, ok := e.srv.Last("approveChatJoinRequest"); !ok {
		t.Fatal("request not approved")
	}
	// The resulting member update must not start a second verification.
	e.srv.SetMember(chatID, tg.ChatMember{Status: tg.StatusMember, User: u})
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: u, ViaJoinRequest: true,
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u}, NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: u}})
	if _, err := e.st.OpenGateFor(e.ctx, chatID, userID); err == nil {
		t.Fatal("approved applicant verified twice")
	}
}

func TestJoinRequestUnreachableGoesToOwnerReview(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Fail("sendPhoto", tgtest.Fault{Code: 403, Description: "Forbidden: bot can't initiate conversation with a user"})
	e.eng.OnJoinRequest(e.ctx, e.chat, &tg.ChatJoinRequest{Chat: tg.Chat{ID: chatID}, From: user(userID), UserChatID: userID})
	e.tick()
	g := e.gate(userID)
	if g.State != store.StateReview || g.ReviewChat != ownerID {
		t.Fatalf("state %s review chat %d", g.State, g.ReviewChat)
	}
}

func TestLeavingClosesWithoutBan(t *testing.T) {
	e := newEnv(t, nil)
	e.join(userID, 0)
	u := user(userID)
	e.srv.SetMember(chatID, tg.ChatMember{Status: tg.StatusLeft, User: u})
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: u,
		OldChatMember: tg.ChatMember{Status: tg.StatusRestricted, IsMember: true, User: u},
		NewChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u}})
	g := e.gate(userID)
	if g.Outcome != store.OutcomeLeft {
		t.Fatalf("outcome %s", g.Outcome)
	}
	if _, ok := e.srv.Last("banChatMember"); ok {
		t.Fatal("left user was banned")
	}
}

func TestExemptions(t *testing.T) {
	e := newEnv(t, nil)
	e.join(userID, adminID) // added by an admin
	if _, err := e.st.OpenGateFor(e.ctx, chatID, userID); err == nil {
		t.Fatal("member added by admin was verified")
	}
	e.join(ownerID, 0)
	if _, err := e.st.OpenGateFor(e.ctx, chatID, ownerID); err == nil {
		t.Fatal("owner was verified")
	}
	// Trusted invite link.
	u := user(5555)
	e.srv.SetMember(chatID, tg.ChatMember{Status: tg.StatusMember, User: u})
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: u,
		InviteLink:    &tg.ChatInviteLink{Name: "朋友 #免验证", Creator: user(adminID)},
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u}, NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: u}})
	if _, err := e.st.OpenGateFor(e.ctx, chatID, 5555); err == nil {
		t.Fatal("trusted link member was verified")
	}
}

func TestBotsAddedByMembersAreRemoved(t *testing.T) {
	e := newEnv(t, nil)
	bot := tg.User{ID: 8888, IsBot: true, Username: "spam_bot"}
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: user(userID),
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: bot}, NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: bot}})
	if ban, ok := e.srv.Last("banChatMember"); !ok || ban.Int("user_id") != 8888 {
		t.Fatal("bot not removed")
	}
	e.srv.Reset()
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: user(adminID),
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: bot}, NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: bot}})
	if _, ok := e.srv.Last("banChatMember"); ok {
		t.Fatal("bot added by admin was removed")
	}
}

func TestRaidLockdown(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Raid.Threshold = 3; s.Raid.Minutes = 10 })
	for i := int64(0); i < 5; i++ {
		e.join(10_000+i, 0)
	}
	e.reloadChat()
	if !e.chat.InLockdown(e.now) {
		t.Fatal("lockdown not triggered")
	}
	early, late := e.gate(10_000), e.gate(10_004)
	if early.Has(store.FlagLockdown) || !late.Has(store.FlagLockdown) {
		t.Fatalf("lockdown flags: early %v late %v", early.Has(store.FlagLockdown), late.Has(store.FlagLockdown))
	}
	if late.PromptMsg != 0 || late.EphMsg == 0 {
		t.Fatalf("lockdown gate should use ephemeral only: prompt %d eph %d", late.PromptMsg, late.EphMsg)
	}
	if late.Deadline.Sub(late.CreatedAt) > 90*time.Second {
		t.Fatal("lockdown did not shorten the deadline")
	}
	// The shared button opens the right user's challenge.
	e.press(10_004, "gs:"+strconv.FormatInt(chatID, 10), &tg.Message{Chat: tg.Chat{ID: chatID}, MessageID: 1})
	if p, ok := e.srv.Last("sendPhoto"); !ok || p.Obj("ephemeral_message_parameters")["callback_query_id"] != "cq" {
		t.Fatal("shared start did not open an ephemeral challenge")
	}
}

func TestPublicDelivery(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Delivery = store.DeliveryPublic })
	e.join(userID, 0)
	g := e.gate(userID)
	if g.CaptchaChat != chatID || g.CaptchaMsg == 0 || g.EphMsg != 0 {
		t.Fatalf("public delivery: %+v", g)
	}
	msg := &tg.Message{Chat: tg.Chat{ID: chatID}, MessageID: g.CaptchaMsg}
	e.answerViaButtons(g, wrong(t, g), msg)
	if _, ok := e.srv.Last("editMessageMedia"); !ok {
		t.Fatal("wrong answer did not swap the image")
	}
	g = e.gate(userID)
	e.answerViaButtons(g, solve(t, g), msg)
	if g = e.gate(userID); g.Outcome != store.OutcomePass {
		t.Fatalf("outcome %s", g.Outcome)
	}
}

func TestPrivateDeliveryDeepLink(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Delivery = store.DeliveryPrivate; s.Verify.Kind = "button" })
	e.join(userID, 0)
	g := e.gate(userID)
	prompt, _ := e.srv.Last("sendMessage")
	if !strings.Contains(prompt.Str("reply_markup"), "start=v"+g.Token) {
		t.Fatal("prompt lacks deep link")
	}
	u := user(userID)
	stranger := user(31337)
	e.eng.OnPrivateStart(e.ctx, &stranger, g.Token)
	if m, _ := e.srv.Last("sendMessage"); !strings.Contains(m.Str("text"), "不属于你") {
		t.Fatal("stranger could open someone else's challenge")
	}
	e.eng.OnPrivateStart(e.ctx, &u, g.Token)
	g = e.gate(userID)
	if g.CaptchaChat != userID {
		t.Fatal("private challenge not sent")
	}
	e.answerViaButtons(g, solve(t, g), &tg.Message{Chat: tg.Chat{ID: userID, Type: tg.ChatPrivate}, MessageID: g.CaptchaMsg})
	if g = e.gate(userID); g.Outcome != store.OutcomePass {
		t.Fatalf("outcome %s", g.Outcome)
	}
}

func TestWebAppFlow(t *testing.T) {
	e := newEnv(t, nil)
	e.eng.cfg.WebURL = "https://verify.example"
	e.join(userID, 0)
	g := e.gate(userID)
	if _, err := e.eng.WebOpen(e.ctx, g.Token, 31337, ""); err != ErrForbidden {
		t.Fatalf("stranger open: %v", err)
	}
	v, err := e.eng.WebOpen(e.ctx, g.Token, userID, "")
	if err != nil || v.Status != StatusChallenge || !strings.HasPrefix(v.Image, "data:image/png;base64,") || v.Picks != 3 {
		t.Fatalf("open: %+v %v", v, err)
	}
	if _, err := e.eng.WebAnswer(e.ctx, g.Token, userID, "", v.Gen+1, []int{0, 1, 2}); err != ErrStale {
		t.Fatalf("stale answer: %v", err)
	}
	v, err = e.eng.WebAnswer(e.ctx, g.Token, userID, "", v.Gen, wrong(t, g))
	if err != nil || v.Status != StatusChallenge || v.AttemptsLeft != 2 || v.Gen != 2 {
		t.Fatalf("wrong answer: %+v %v", v, err)
	}
	g = e.gate(userID)
	v, err = e.eng.WebAnswer(e.ctx, g.Token, userID, "", g.Gen, solve(t, g))
	if err != nil || v.Status != StatusPassed {
		t.Fatalf("correct answer: %+v %v", v, err)
	}
	if v, _ = e.eng.WebOpen(e.ctx, g.Token, userID, ""); v.Status != StatusPassed {
		t.Fatalf("reopen after pass: %+v", v)
	}
}

func TestTransientFailureRetriesAndRecovers(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Kind = "button" })
	e.srv.Fail("restrictChatMember", tgtest.Fault{Code: 500, Description: "Internal Server Error", Times: 1})
	e.join(userID, 0)
	g := e.gate(userID)
	if g.State != store.StateNew || g.Retries != 1 || !g.NextAt.After(e.now) {
		t.Fatalf("retry not scheduled: %+v", g)
	}
	e.advance(time.Minute)
	e.eng.Process(e.ctx, g.ID)
	if g = e.gate(userID); g.State != store.StateChallenge {
		t.Fatalf("did not recover: %s (%s)", g.State, g.LastError)
	}
}

func TestMissingRightsCancelsAndAlertsOwner(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Fail("restrictChatMember", tgtest.Fault{Code: 400, Description: "Bad Request: not enough rights to restrict/unrestrict chat member"})
	e.join(userID, 0)
	g := e.gate(userID)
	if g.Outcome != store.OutcomeCancel {
		t.Fatalf("outcome %s", g.Outcome)
	}
	alerted := false
	for _, c := range e.srv.Calls("sendMessage") {
		if c.Int("chat_id") == ownerID && strings.Contains(c.Str("text"), "restrict") {
			alerted = true
		}
	}
	if !alerted {
		t.Fatal("owner not alerted")
	}
}

func TestPromotedWhileVerifyingIsCancelled(t *testing.T) {
	e := newEnv(t, nil)
	e.join(userID, 0)
	u := user(userID)
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: user(ownerID),
		OldChatMember: tg.ChatMember{Status: tg.StatusRestricted, IsMember: true, User: u},
		NewChatMember: tg.ChatMember{Status: tg.StatusAdministrator, User: u}})
	if g := e.gate(userID); g.Outcome != store.OutcomeCancel {
		t.Fatalf("outcome %s", g.Outcome)
	}
}

func TestPausedChatSkipsNewcomers(t *testing.T) {
	e := newEnv(t, nil)
	_ = e.st.SetChatStatus(e.ctx, chatID, store.ChatPaused)
	e.join(userID, 0)
	if _, err := e.st.OpenGateFor(e.ctx, chatID, userID); err == nil {
		t.Fatal("paused chat verified a newcomer")
	}
}

func TestSchedulerRunsDueGates(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Timeout = 60 })
	e.join(userID, 0)
	e.advance(2 * time.Minute)
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})
	go func() { _ = e.eng.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if g := e.gate(userID); g.State == store.StateClosed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if g := e.gate(userID); g.Outcome != store.OutcomeTimeout {
		t.Fatalf("scheduler did not time out the gate: %s/%s", g.State, g.Outcome)
	}
}

func TestParseButtons(t *testing.T) {
	kb := ParseButtons("Rules - https://t.me/x/1 | Chan - https://t.me/x\nbad line\nSite - javascript:alert(1)")
	if kb == nil || len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 2 {
		t.Fatalf("buttons: %+v", kb)
	}
	text, _ := RenderWelcome(store.Welcome{Text: "Hi {mention} in {group}"}, "en", 5, "<Eve>", "", "G&G")
	if !strings.Contains(text, "&lt;Eve&gt;") || !strings.Contains(text, "G&amp;G") {
		t.Fatalf("welcome not escaped: %s", text)
	}
}

func TestServiceMessageFallbackDoesNotOverrideExemption(t *testing.T) {
	e := newEnv(t, nil)
	u := user(5555)
	e.srv.SetMember(chatID, tg.ChatMember{Status: tg.StatusMember, User: u})
	// Service message first: a fallback gate starts and mutes.
	e.eng.OnNewMembers(e.ctx, e.chat, &tg.Message{From: &u, Chat: tg.Chat{ID: chatID}, NewChatMembers: []tg.User{u}})
	if g := e.gate(5555); !g.Has(store.FlagFallback) {
		t.Fatal("fallback gate not flagged")
	}
	// The richer update reveals a trusted link: the gate is undone.
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: u,
		InviteLink:    &tg.ChatInviteLink{Name: "#noverify", Creator: user(adminID)},
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u}, NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: u}})
	if g := e.gate(5555); g.Outcome != store.OutcomeCancel {
		t.Fatalf("fallback gate outcome %s", g.Outcome)
	}
	if m := e.srv.Member(chatID, 5555); m.Status != tg.StatusMember {
		t.Fatalf("exempt member still restricted: %s", m.Status)
	}
	// And the other order: exemption first, service message afterwards.
	v := user(6666)
	e.srv.SetMember(chatID, tg.ChatMember{Status: tg.StatusMember, User: v})
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: v,
		InviteLink:    &tg.ChatInviteLink{Name: "#免验证", Creator: user(adminID)},
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: v}, NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: v}})
	e.eng.OnNewMembers(e.ctx, e.chat, &tg.Message{From: &v, Chat: tg.Chat{ID: chatID}, NewChatMembers: []tg.User{v}})
	if _, err := e.st.OpenGateFor(e.ctx, chatID, 6666); err == nil {
		t.Fatal("service message re-verified an exempt member")
	}
}

func TestDirectJoinWhileRequestPendingIsVerified(t *testing.T) {
	e := newEnv(t, nil)
	e.eng.OnJoinRequest(e.ctx, e.chat, &tg.ChatJoinRequest{Chat: tg.Chat{ID: chatID}, From: user(userID), UserChatID: userID})
	e.tick()
	if g := e.gate(userID); g.Source != store.SourceRequest {
		t.Fatal("no request gate")
	}
	e.join(userID, 0)
	g := e.gate(userID)
	if g.Source != store.SourceJoin || !g.Open() {
		t.Fatalf("direct join not verified: %+v", g)
	}
	if m := e.srv.Member(chatID, userID); m.Status != tg.StatusRestricted {
		t.Fatalf("member not muted: %s", m.Status)
	}
}

func TestLeaveAndRejoinDoesNotRestoreOwnMute(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Kind = "button" })
	e.join(userID, 0)
	u := user(userID)
	// The member leaves mid-way; Telegram keeps the restriction record.
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: u,
		OldChatMember: tg.ChatMember{Status: tg.StatusRestricted, IsMember: true, User: u},
		NewChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u}})
	if r, _ := e.srv.Last("restrictChatMember"); r.Obj("permissions")["can_send_messages"] != true {
		t.Fatal("mute not lifted after leaving")
	}
	// Simulate Telegram still reporting the old full mute on rejoin.
	e.advance(time.Minute)
	e.srv.SetMember(chatID, tg.ChatMember{Status: tg.StatusRestricted, IsMember: true, User: u, ChatPermissions: tg.Muted()})
	e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{Chat: tg.Chat{ID: chatID}, From: u,
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u}, NewChatMember: tg.ChatMember{Status: tg.StatusRestricted, IsMember: true, User: u}})
	e.tick()
	g := e.gate(userID)
	if g.PrevPerms != "" {
		t.Fatalf("own mute captured as admin restriction: %s", g.PrevPerms)
	}
	e.answerViaButtons(g, solve(t, g), ephMsg(g))
	if m := e.srv.Member(chatID, userID); m.Status != tg.StatusMember {
		t.Fatalf("member still restricted after passing: %s", m.Status)
	}
}

func TestUndeliverableReviewTimeoutLetsUserIn(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Delivery = store.DeliveryPublic; s.Verify.ReviewTTL = 300 })
	e.srv.Fail("sendPhoto", tgtest.Fault{Code: 400, Description: "Bad Request: not enough rights to send photos"})
	e.join(userID, 0)
	g := e.gate(userID)
	if g.State != store.StateReview || g.LastError != "undeliverable" {
		t.Fatalf("state %s reason %q", g.State, g.LastError)
	}
	e.advance(301 * time.Second)
	e.tick()
	if g = e.gate(userID); g.Outcome != store.OutcomeCancel {
		t.Fatalf("outcome %s", g.Outcome)
	}
	if _, ok := e.srv.Last("banChatMember"); ok {
		t.Fatal("user punished for a bot-side delivery failure")
	}
}

func TestGuardExpiredQueryFallsBackToApprove(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.Kind = "button" })
	e.eng.cfg.WebURL = "https://verify.example"
	e.eng.OnJoinRequest(e.ctx, e.chat, &tg.ChatJoinRequest{Chat: tg.Chat{ID: chatID}, From: user(userID), UserChatID: userID, QueryID: "q1"})
	if _, ok := e.srv.Last("sendChatJoinRequestWebApp"); !ok {
		t.Fatal("guard web app not shown")
	}
	g := e.gate(userID)
	if g.Source != store.SourceGuard {
		t.Fatalf("source %s", g.Source)
	}
	e.srv.Fail("answerChatJoinRequestQuery", tgtest.Fault{Code: 400, Description: "Bad Request: query is too old"})
	v, err := e.eng.WebAnswer(e.ctx, g.Token, userID, "q1", g.Gen, solve(t, g))
	if err != nil || v.Status != StatusPassed {
		t.Fatalf("answer: %+v %v", v, err)
	}
	if _, ok := e.srv.Last("approveChatJoinRequest"); !ok {
		t.Fatal("no fallback approval")
	}
}
