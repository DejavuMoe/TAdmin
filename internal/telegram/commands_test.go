package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	telegrambot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"tadmin/internal/config"
	"tadmin/internal/store"
)

func TestVerificationCommandsAndSetup(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mu sync.Mutex
	var methods, destinations []string
	forum := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		mu.Lock()
		defer mu.Unlock()
		methods = append(methods, method)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getChat":
			id := r.FormValue("chat_id")
			fmt.Fprintf(w, `{"ok":true,"result":{"id":%s,"type":"supergroup","title":"Group","is_forum":%t}}`, id, forum)
		case "getChatMember":
			fmt.Fprint(w, `{"ok":true,"result":{"status":"administrator","user":{"id":123,"is_bot":true,"first_name":"Bot"},"can_restrict_members":true,"can_invite_users":true,"can_manage_topics":false,"can_delete_messages":false}}`)
		case "sendMessage", "editMessageText":
			if r.FormValue("message_thread_id") != "" {
				t.Error("unexpected topic routing")
			}
			destinations = append(destinations, r.FormValue("chat_id"))
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":501,"date":1,"chat":{"id":-2001,"type":"supergroup"},"text":"ok"}}`)
		default:
			t.Errorf("unexpected method %s", method)
			http.Error(w, "unexpected", 400)
		}
	}))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{OwnerUserID: 1, ManagementChatID: -2001, PublicBaseURL: "https://verify.example.test", DefaultLanguage: "en", BotToken: "123:test"}
	handler := newCommandHandler(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)), BotInfo{ID: 123, Username: "TestBot"}, "test", time.Now(), nil)
	command := func(actor int64, text string) {
		handler.handle(ctx, bot, &models.Update{Message: &models.Message{ID: 1, Chat: models.Chat{ID: actor, Type: models.ChatTypePrivate}, From: &models.User{ID: actor}, Text: text, Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: 7}}}})
	}
	command(2, "/tadmin setup -1001")
	if _, err := db.GetManagedChat(ctx, -1001); err != store.ErrManagedChatNotFound {
		t.Fatalf("non-owner setup: %v", err)
	}
	for range 2 {
		command(1, "/tadmin setup -1001")
	}
	chat, err := db.GetManagedChat(ctx, -1001)
	if err != nil || !chat.Enabled || chat.ManagementTopicID != 0 || chat.ManagementChatID != -2001 || chat.ReentryTrustSeconds != 0 {
		t.Fatalf("chat=%+v err=%v", chat, err)
	}
	command(1, "/tadmin timeout -1001 90")
	chat, _ = db.GetManagedChat(ctx, -1001)
	if chat.VerificationTimeoutSeconds != 90 {
		t.Fatal(chat)
	}
	command(2, "/tadmin timeout -1001 60")
	command(1, "/tadmin timeout -1001 1")
	chat, _ = db.GetManagedChat(ctx, -1001)
	if chat.VerificationTimeoutSeconds != 90 {
		t.Fatal("invalid/unauthorized timeout changed state")
	}
	for _, text := range []string{"/tadmin groups", "/tadmin status", "/tadmin raid on", "/tadmin invite create test", "/tadmin ban 42", "/help"} {
		command(1, text)
	}
	mu.Lock()
	forum = true
	mu.Unlock()
	command(1, "/tadmin setup -1002")
	if _, err := db.GetManagedChat(ctx, -1002); err != store.ErrManagedChatNotFound {
		t.Fatalf("forum accepted: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, m := range methods {
		if m == "createForumTopic" || m == "banChatMember" || m == "createChatInviteLink" {
			t.Fatal(m)
		}
	}
	if destinations[len(destinations)-1] != "1" {
		t.Fatal("private command reply sent to source group")
	}
}

func TestMembershipUsesRestrictedIsMember(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want models.ChatMemberType
	}{
		{`{"status":"restricted","is_member":false,"user":{"id":42}}`, models.ChatMemberTypeLeft},
		{`{"status":"restricted","is_member":true,"user":{"id":42}}`, models.ChatMemberTypeRestricted},
		{`{"status":"member","user":{"id":42}}`, models.ChatMemberTypeMember},
	} {
		var member models.ChatMember
		if err := json.Unmarshal([]byte(tc.raw), &member); err != nil {
			t.Fatal(err)
		}
		if got := membershipStatus(member); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}

func TestReviewDestinationDoesNotRequireAnotherGroup(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		configured, destination int64
		blocked                 bool
	}{
		{"default owner private chat", 0, 1, false},
		{"same source group", -1001, -1001, false},
		{"optional shared group", -2001, -2001, false},
		{"owner has not started bot", 0, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "routing.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var mu sync.Mutex
			var probed bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				w.Header().Set("Content-Type", "application/json")
				switch method {
				case "getChat":
					id := r.FormValue("chat_id")
					if id == "1" {
						t.Error("owner private destination must not require group lookup")
					}
					fmt.Fprintf(w, `{"ok":true,"result":{"id":%s,"type":"supergroup","title":"Test"}}`, id)
				case "getChatMember":
					if r.FormValue("chat_id") != "-1001" {
						t.Error("review destination must not require administrator permission")
					}
					fmt.Fprint(w, `{"ok":true,"result":{"status":"administrator","user":{"id":123,"is_bot":true},"can_restrict_members":true,"can_invite_users":true}}`)
				case "sendMessage", "editMessageText":
					if tc.blocked {
						fmt.Fprint(w, `{"ok":false,"error_code":403,"description":"bot cannot initiate conversation"}`)
						return
					}
					if r.FormValue("text") == "TAdmin setup check" {
						mu.Lock()
						probed = true
						mu.Unlock()
						if r.FormValue("chat_id") != fmt.Sprint(tc.destination) {
							t.Error("incorrect review destination")
						}
					}
					fmt.Fprintf(w, `{"ok":true,"result":{"message_id":50,"date":1,"chat":{"id":%d,"type":"private"}}}`, tc.destination)
				default:
					t.Error("unexpected method", method)
				}
			}))
			defer server.Close()
			bot, err := telegrambot.New("123:offline", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{OwnerUserID: 1, ManagementChatID: tc.configured, PublicBaseURL: "https://verify.example.test", DefaultLanguage: "en", BotToken: "123:offline"}
			handler := newCommandHandler(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)), BotInfo{ID: 123}, "test", time.Now(), nil)
			handler.setup(ctx, bot, &models.Message{Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup}})
			chat, err := db.GetManagedChat(ctx, -1001)
			if tc.blocked {
				if err != store.ErrManagedChatNotFound {
					t.Fatal("enabled without a reachable review destination")
				}
				return
			}
			mu.Lock()
			sent := probed
			mu.Unlock()
			if err != nil || !chat.Enabled || chat.ManagementChatID != tc.destination || !sent {
				t.Fatalf("setup destination not persisted: chat=%+v err=%v", chat, err)
			}
		})
	}
}
