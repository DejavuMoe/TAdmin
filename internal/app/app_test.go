package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"tadmin/internal/config"
	"tadmin/internal/tg"
	"tadmin/internal/tgtest"
)

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// TestRunEndToEnd boots the whole application against the fake Bot API,
// lets a newcomer join a group through getUpdates and checks the result.
func TestRunEndToEnd(t *testing.T) {
	srv := tgtest.New(t)
	const group, owner, newcomer = int64(-100900), int64(1), int64(4242)
	srv.SetAdmins(group, owner)
	listen := freePort(t)
	env := map[string]string{
		"TADMIN_BOT_TOKEN": "123:TEST", "TADMIN_OWNER_ID": "1", "TADMIN_DATA_DIR": t.TempDir(),
		"TADMIN_BOT_API_URL": srv.URL, "TADMIN_LISTEN": listen, "TADMIN_TIMEZONE": "UTC",
	}
	cfg, err := config.Parse(nil, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, &cfg, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			cancel()
			<-done
		}
	})

	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
	}
	waitFor("owner greeting", func() bool {
		for _, c := range srv.Calls("sendMessage") {
			if c.Int("chat_id") == owner && strings.Contains(c.Str("text"), "TAdmin") {
				return true
			}
		}
		return false
	})
	if len(srv.Calls("setMyCommands")) == 0 {
		t.Fatal("commands not registered")
	}
	resp, err := http.Get("http://" + listen + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	resp.Body.Close()

	// The owner adds the bot, then a newcomer joins.
	bot := srv.Bot
	srv.Push(tg.Update{MyChatMember: &tg.ChatMemberUpdated{
		Chat: tg.Chat{ID: group, Type: tg.ChatSupergroup, Title: "E2E"}, From: tg.User{ID: owner, FirstName: "Owner"},
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: bot},
		NewChatMember: tg.ChatMember{Status: tg.StatusAdministrator, User: bot, CanRestrictMembers: true},
	}})
	u := tg.User{ID: newcomer, FirstName: "New"}
	srv.SetMember(group, tg.ChatMember{Status: tg.StatusMember, User: u})
	srv.Push(tg.Update{ChatMember: &tg.ChatMemberUpdated{
		Chat: tg.Chat{ID: group, Type: tg.ChatSupergroup}, From: u,
		OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u}, NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: u},
	}})
	waitFor("newcomer muted", func() bool { return srv.Member(group, newcomer).Status == tg.StatusRestricted })
	waitFor("ephemeral challenge", func() bool {
		for _, c := range srv.Calls("sendPhoto") {
			if c.Obj("ephemeral_message_parameters") != nil {
				return true
			}
		}
		return false
	})

	cancel()
	stopped = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
}
