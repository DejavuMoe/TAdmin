package tg

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCallDecodesResultAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			io.WriteString(w, `{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"T","username":"tbot"}}`)
		case strings.HasSuffix(r.URL.Path, "/deleteMessage"):
			io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: message to delete not found"}`)
		default:
			io.WriteString(w, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
		}
	}))
	defer srv.Close()
	c := New("123:secret", WithBaseURL(srv.URL))
	me, err := c.GetMe(context.Background())
	if err != nil || me.ID != 42 || me.Username != "tbot" {
		t.Fatalf("GetMe = %+v, %v", me, err)
	}
	err = c.DeleteMessage(context.Background(), -1, 5)
	if !IsGone(err) || !IsPermanent(err) {
		t.Fatalf("expected gone error, got %v", err)
	}
	_, err = c.SendMessage(context.Background(), SendMessageParams{ChatID: 1, Text: "x"})
	if !IsForbidden(err) {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestRetryAfter429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`)
			return
		}
		io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer srv.Close()
	c := New("t", WithBaseURL(srv.URL))
	start := time.Now()
	if err := c.DeleteMessage(context.Background(), -100, 1); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("expected one retry after ~1s, calls=%d elapsed=%v", calls.Load(), time.Since(start))
	}
}

func TestTokenNeverLeaksInErrors(t *testing.T) {
	c := New("123:supersecret", WithBaseURL("http://127.0.0.1:1"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.GetMe(ctx)
	if err == nil || strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("error leaks token or nil: %v", err)
	}
}

func TestMultipartUpload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse: %v", err)
		}
		if r.FormValue("chat_id") != "-100" || r.FormValue("caption") != "hi" {
			t.Errorf("fields: %v", r.MultipartForm.Value)
		}
		var kb InlineKeyboard
		if err := json.Unmarshal([]byte(r.FormValue("reply_markup")), &kb); err != nil || kb.InlineKeyboard[0][0].CallbackData != "x" {
			t.Errorf("reply_markup: %q %v", r.FormValue("reply_markup"), err)
		}
		f, _, err := r.FormFile("photo")
		if err != nil {
			t.Errorf("photo: %v", err)
		} else {
			b, _ := io.ReadAll(f)
			if string(b) != "PNG" {
				t.Errorf("photo body %q", b)
			}
		}
		io.WriteString(w, `{"ok":true,"result":{"message_id":7,"date":0,"chat":{"id":-100,"type":"supergroup"}}}`)
	}))
	defer srv.Close()
	c := New("t", WithBaseURL(srv.URL))
	m, err := c.SendPhoto(context.Background(), SendPhotoParams{ChatID: -100, Caption: "hi", ReplyMarkup: Keyboard(Row(CB("a", "x")))}, []byte("PNG"))
	if err != nil || m.MessageID != 7 {
		t.Fatalf("SendPhoto = %+v, %v", m, err)
	}
}

func TestCommandArgs(t *testing.T) {
	cases := []struct{ in, cmd, target, args string }{
		{"/start", "start", "", ""},
		{"/Mute@TBot 10m spam", "mute", "TBot", "10m spam"},
		{"/start v_abc", "start", "", "v_abc"},
		{"/note\nline", "note", "", "line"},
		{"hello", "", "", ""},
	}
	for _, c := range cases {
		cmd, target, args := CommandArgs(c.in)
		if cmd != c.cmd || target != c.target || args != c.args {
			t.Errorf("CommandArgs(%q) = %q %q %q", c.in, cmd, target, args)
		}
	}
}

func TestMentionEscapes(t *testing.T) {
	got := MentionID(5, "<b>x</b>")
	if !strings.Contains(got, "&lt;b&gt;") || !strings.Contains(got, "tg://user?id=5") {
		t.Fatalf("Mention = %s", got)
	}
}

func TestTimedMethodsDoNotRetry429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 20","parameters":{"retry_after":20}}`)
	}))
	defer srv.Close()
	c := New("t", WithBaseURL(srv.URL))
	err := c.BanChatMember(context.Background(), -100, 1, time.Now().Add(2*time.Minute), false)
	if !IsTooManyRequests(err) || calls.Load() != 1 {
		t.Fatalf("ban retried internally: calls=%d err=%v", calls.Load(), err)
	}
}
