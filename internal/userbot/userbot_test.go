package userbot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestClassifyReply(t *testing.T) {
	cases := []struct {
		text string
		want Outcome
	}{
		{"Link previews was updated successfully.", OutcomeUpdated},
		{"The preview has been REFRESHED", OutcomeUpdated},
		{"Sorry, the link preview couldn’t be updated.", OutcomeNoPreview},
		{"No preview available for this link", OutcomeNoPreview},
		{"Page not found", OutcomeNoPreview},
		{"Could not fetch the page", OutcomeNoPreview},
		{"Unable to generate a preview", OutcomeNoPreview},
		{"Invalid URL", OutcomeNoPreview},
		{"Hello! Send me a link.", OutcomeUnknown},
		{"   ", OutcomeUnknown},
		{"", OutcomeUnknown},
	}
	for _, c := range cases {
		if got := ClassifyReply(c.text); got != c.want {
			t.Errorf("ClassifyReply(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"8613812341234":     "+86*******1234",
		"+86 138-1234-1234": "+86*******1234",
		"12345":             "+***45",
		"1234":              "+****",
		"4915112345678":     "+49*******5678",
	}
	for in, want := range cases {
		if got := MaskPhone(in); got != want {
			t.Errorf("MaskPhone(%q) = %q, want %q", in, got, want)
		}
		if in != "" && strings.Contains(MaskPhone(in), "3812") {
			t.Errorf("MaskPhone(%q) leaks middle digits", in)
		}
	}
}

func TestNormalizeCode(t *testing.T) {
	cases := map[string]string{
		"12345":         "12345",
		"1 2 3 4 5":     "12345",
		"12-345":        "12345",
		" 12.345\n":     "12345",
		"１２３４５":         "12345",
		"code: 12 345!": "code12345",
	}
	for in, want := range cases {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizePhone(t *testing.T) {
	ok := map[string]string{
		"+86 138 1234 1234": "8613812341234",
		"+1 (555) 123-4567": "15551234567",
		"0044 20 7946 0958": "442079460958",
		"４９１５１１２３４５６７８":     "4915112345678",
	}
	for in, want := range ok {
		got, err := NormalizePhone(in)
		if err != nil || got != want {
			t.Errorf("NormalizePhone(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "123", "abc12345678", "+0123456789", "1234567890123456"} {
		if _, err := NormalizePhone(in); !errors.Is(err, ErrPhoneInvalid) {
			t.Errorf("NormalizePhone(%q) err = %v, want ErrPhoneInvalid", in, err)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("héllo wörld", 5); got != "héllo" {
		t.Fatalf("got %q", got)
	}
	if got := truncateRunes("短", 5); got != "短" {
		t.Fatalf("got %q", got)
	}
	if got := truncateRunes(strings.Repeat("链", 400), 300); len([]rune(got)) != 300 {
		t.Fatalf("got %d runes", len([]rune(got)))
	}
}

func TestRenderQR(t *testing.T) {
	data, err := RenderQR("tg://login?token=" + strings.Repeat("QUJDREVGR0hJSktMTU5PUA", 2))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() {
		t.Fatalf("not square: %v", b)
	}
	if b.Dx() < 400 || b.Dx() > 640 {
		t.Fatalf("unexpected size %d", b.Dx())
	}
	isBlack := func(x, y int) bool {
		r, g, bl, _ := img.At(x, y).RGBA()
		return r == 0 && g == 0 && bl == 0
	}
	if isBlack(0, 0) || isBlack(b.Dx()-1, b.Dy()-1) {
		t.Fatal("quiet zone is not white")
	}
	// The top-left finder pattern starts right after the 4-module quiet zone.
	var firstBlack int = -1
	for x := 0; x < b.Dx()/2; x++ {
		if isBlack(x, x) {
			firstBlack = x
			break
		}
	}
	if firstBlack <= 0 {
		t.Fatal("finder pattern not found")
	}
	if c := color.GrayModel.Convert(img.At(firstBlack-1, firstBlack-1)).(color.Gray); c.Y != 255 {
		t.Fatal("expected white before finder pattern")
	}
}

type errStore struct{ err error }

func (s errStore) LoadSession(context.Context) ([]byte, error) { return nil, s.err }
func (s errStore) StoreSession(context.Context, []byte) error  { return s.err }

func TestSessionAdapter(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	a := &sessionAdapter{store: store}

	if _, err := a.LoadSession(ctx); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("empty load err = %v, want session.ErrNotFound", err)
	}
	if a.loaded.Load() {
		t.Fatal("loaded set for empty store")
	}
	if err := a.StoreSession(ctx, []byte("blob")); err != nil {
		t.Fatal(err)
	}
	got, err := a.LoadSession(ctx)
	if err != nil || string(got) != "blob" {
		t.Fatalf("load = %q, %v", got, err)
	}
	if !a.loaded.Load() {
		t.Fatal("loaded not set")
	}

	// A disabled adapter neither loads nor writes.
	a.disabled.Store(true)
	if err := a.StoreSession(ctx, []byte("late")); err != nil {
		t.Fatal(err)
	}
	if raw, _ := store.LoadSession(ctx); string(raw) != "blob" {
		t.Fatalf("disabled adapter wrote %q", raw)
	}
	if _, err := a.LoadSession(ctx); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("disabled load err = %v", err)
	}

	boom := errors.New("boom")
	b := &sessionAdapter{store: errStore{err: boom}}
	if _, err := b.LoadSession(ctx); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestMapError(t *testing.T) {
	cases := []struct {
		err  error
		want error
	}{
		{tgerr.New(400, "PHONE_NUMBER_INVALID"), ErrPhoneInvalid},
		{tgerr.New(400, "PHONE_NUMBER_BANNED"), ErrPhoneBanned},
		{tgerr.New(400, "PHONE_CODE_INVALID"), ErrInvalidCode},
		{tgerr.New(400, "PHONE_CODE_EXPIRED"), ErrCodeExpired},
		{tgerr.New(400, "PASSWORD_HASH_INVALID"), ErrPasswordInvalid},
		{auth.ErrPasswordInvalid, ErrPasswordInvalid},
		{tgerr.New(401, "SESSION_PASSWORD_NEEDED"), errPasswordNeeded},
		{auth.ErrPasswordAuthNeeded, errPasswordNeeded},
		{tgerr.New(401, "AUTH_KEY_UNREGISTERED"), ErrAuthLost},
		{tgerr.New(401, "SESSION_REVOKED"), ErrNotReady},
		{fmt.Errorf("sign in: %w", tgerr.New(400, "PHONE_CODE_INVALID")), ErrInvalidCode},
		{context.Canceled, context.Canceled},
	}
	for _, c := range cases {
		if got := mapError(c.err); !errors.Is(got, c.want) {
			t.Errorf("mapError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
	if mapError(nil) != nil {
		t.Error("mapError(nil) != nil")
	}

	other := tgerr.New(400, "MESSAGE_EMPTY")
	if got := mapError(other); got != other {
		t.Errorf("unknown error changed: %v", got)
	}
	// Mapped errors keep the underlying cause.
	if got := mapError(tgerr.New(400, "PHONE_CODE_INVALID")); !tgerr.Is(got, "PHONE_CODE_INVALID") {
		t.Errorf("cause lost: %v", got)
	}

	for msg, want := range map[string]time.Duration{
		"FLOOD_WAIT_30":        30 * time.Second,
		"FLOOD_PREMIUM_WAIT_5": 5 * time.Second,
		"SLOWMODE_WAIT_120":    2 * time.Minute,
	} {
		var fw *FloodWaitError
		got := mapError(fmt.Errorf("send: %w", tgerr.New(420, msg)))
		if !errors.As(got, &fw) || fw.Wait != want {
			t.Errorf("mapError(%s) = %v, want flood wait %s", msg, got, want)
		}
	}
}

func TestSentMessageID(t *testing.T) {
	if got := sentMessageID(&tg.UpdateShortSentMessage{ID: 42}, 1); got != 42 {
		t.Fatalf("short: %d", got)
	}
	upd := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateMessageID{ID: 7, RandomID: 99},
		&tg.UpdateMessageID{ID: 8, RandomID: 5},
	}}
	if got := sentMessageID(upd, 5); got != 8 {
		t.Fatalf("updates: %d", got)
	}
	upd2 := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewMessage{Message: &tg.Message{ID: 11, Out: true}},
	}}
	if got := sentMessageID(upd2, 5); got != 11 {
		t.Fatalf("new message: %d", got)
	}
	if got := sentMessageID(&tg.UpdatesTooLong{}, 5); got != 0 {
		t.Fatalf("too long: %d", got)
	}
}

func TestFirstReply(t *testing.T) {
	hist := &tg.MessagesMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 13, Message: "second"},
		&tg.Message{ID: 12, Message: "first"},
		&tg.Message{ID: 11, Out: true, Message: "https://example.com"},
		&tg.Message{ID: 9, Message: "old"},
		&tg.MessageService{ID: 14},
	}}
	got := firstReply(hist, 11)
	if got == nil || got.Message != "first" {
		t.Fatalf("got %+v", got)
	}
	if firstReply(hist, 13) != nil {
		t.Fatal("expected no reply")
	}
	if firstReply(&tg.MessagesMessagesNotModified{}, 1) != nil {
		t.Fatal("expected nil for not modified")
	}
}

func TestPeerFromResolved(t *testing.T) {
	res := &tg.ContactsResolvedPeer{
		Peer: &tg.PeerUser{UserID: 5},
		Users: []tg.UserClass{
			&tg.User{ID: 4, AccessHash: 1},
			&tg.User{ID: 5, AccessHash: 77},
		},
	}
	p, err := peerFromResolved(res)
	if err != nil || p.UserID != 5 || p.AccessHash != 77 {
		t.Fatalf("got %+v, %v", p, err)
	}
	if _, err := peerFromResolved(&tg.ContactsResolvedPeer{Peer: &tg.PeerChannel{ChannelID: 1}}); err == nil {
		t.Fatal("expected error for channel")
	}
}

func TestCodeInfoFrom(t *testing.T) {
	s := &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{Length: 5}, PhoneCodeHash: "h"}
	s.SetTimeout(60)
	s.SetNextType(&tg.AuthCodeTypeSMS{})
	info := codeInfoFrom(s)
	if info.Type != "app" || info.Length != 5 || info.Timeout != time.Minute || info.NextType != "sms" {
		t.Fatalf("got %+v", info)
	}
}

func TestQRFlowPushKeepsLatest(t *testing.T) {
	q := &qrFlow{updates: make(chan QRUpdate, 1)}
	q.push(QRUpdate{URL: "a"})
	q.push(QRUpdate{URL: "b"})
	if u := <-q.updates; u.URL != "b" {
		t.Fatalf("got %q", u.URL)
	}
}

func TestManagerOffline(t *testing.T) {
	m := New(Options{}, nil)
	st := m.Status()
	if st.State != StateOffline || st.Self != nil || m.Ready() {
		t.Fatalf("status = %+v", st)
	}
	ctx := context.Background()
	if _, err := m.SubmitCode(ctx, "12345"); !errors.Is(err, ErrNoLogin) {
		t.Fatalf("SubmitCode err = %v", err)
	}
	if _, err := m.SubmitPassword(ctx, "pw"); !errors.Is(err, ErrNoLogin) {
		t.Fatalf("SubmitPassword err = %v", err)
	}
	if _, err := m.StartPhoneLogin(ctx, "not a phone"); !errors.Is(err, ErrPhoneInvalid) {
		t.Fatalf("StartPhoneLogin err = %v", err)
	}
	if err := m.Run(ctx); err == nil {
		t.Fatal("Run without credentials should fail")
	}

	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := m.RefreshPreview(short, "https://example.com"); err == nil {
		t.Fatal("RefreshPreview offline should fail")
	}
	if _, err := m.StartQRLogin(short); err == nil {
		t.Fatal("StartQRLogin offline should fail")
	}
	if _, err := m.RefreshPreview(ctx, " "); err == nil {
		t.Fatal("empty url should fail")
	}
	m.CancelLogin() // no-op
	if err := m.Logout(ctx); err != nil {
		t.Fatalf("offline logout: %v", err)
	}
}

func TestManagerFloodPrecheck(t *testing.T) {
	m := New(Options{}, nil)
	m.sendFlood = time.Now().Add(time.Minute)
	_, err := m.RefreshPreview(context.Background(), "https://example.com")
	var fw *FloodWaitError
	if !errors.As(err, &fw) || fw.Wait <= 0 {
		t.Fatalf("err = %v, want FloodWaitError", err)
	}
}

func TestObserveFloodSetsStatus(t *testing.T) {
	m := New(Options{}, nil)
	err := m.observe(tgerr.New(420, "FLOOD_WAIT_60"))
	var fw *FloodWaitError
	if !errors.As(err, &fw) {
		t.Fatalf("err = %v", err)
	}
	if until := m.Status().FloodUntil; time.Until(until) < 50*time.Second {
		t.Fatalf("FloodUntil = %v", until)
	}
}
