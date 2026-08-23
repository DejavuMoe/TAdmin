package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	telegrambot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"tadmin/internal/config"
	"tadmin/internal/invite"
	"tadmin/internal/moderation"
	"tadmin/internal/store"
)

func TestSetupIsIdempotentAndReusesManagementTopic(t *testing.T) {
	var createdTopics atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		response.Header().Set("Content-Type", "application/json")
		switch method {
		case "getChatMember":
			_, _ = response.Write([]byte(`{"ok":true,"result":{"status":"administrator","user":{"id":123,"is_bot":true,"first_name":"TAdmin"},"can_be_edited":true,"is_anonymous":false,"can_manage_chat":true,"can_delete_messages":true,"can_restrict_members":true,"can_invite_users":true,"can_manage_topics":true}}`))
		case "getChat":
			_, _ = response.Write([]byte(`{"ok":true,"result":{"id":-2001,"type":"supergroup","title":"Management","is_forum":true}}`))
		case "createForumTopic":
			createdTopics.Add(1)
			_, _ = response.Write([]byte(`{"ok":true,"result":{"message_thread_id":77,"name":"TAdmin Source"}}`))
		case "sendMessage":
			_, _ = response.Write([]byte(`{"ok":true,"result":{"message_id":501,"date":1,"chat":{"id":-2001,"type":"supergroup"},"text":"ok"}}`))
		case "editMessageText":
			_, _ = response.Write([]byte(`{"ok":true,"result":{"message_id":501,"date":1,"chat":{"id":-2001,"type":"supergroup"},"text":"ok"}}`))
		default:
			http.Error(response, "unexpected method", http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "tadmin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		OwnerUserID: 1, DatabasePath: "unused", PublicBaseURL: "https://verify.example.test",
		ManagementChatID: -2001, DefaultLanguage: "en", BotToken: "123:test",
	}
	handler := newCommandHandler(cfg, database, slog.New(slog.NewTextHandler(io.Discard, nil)), BotInfo{ID: 123, Username: "TAdminBot", SupportsJoinRequestQueries: true}, "test", time.Now(), nil, nil, nil)
	message := &models.Message{Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup, Title: "Source"}}
	for range 2 {
		handler.setup(ctx, bot, message)
	}
	if createdTopics.Load() != 1 {
		t.Fatalf("created topics = %d", createdTopics.Load())
	}
	managed, err := database.GetManagedChat(ctx, -1001)
	if err != nil {
		t.Fatal(err)
	}
	if !managed.Enabled || managed.ManagementTopicID != 77 {
		t.Fatalf("managed chat = %+v", managed)
	}
}

func TestInviteCommandsAreOwnerOnlyAndKeepSecretOutOfGroup(t *testing.T) {
	const secretURL = "https://t.me/+command-secret"
	var createCalls, revokeCalls atomic.Int32
	var mu sync.Mutex
	var groupTexts []string
	var managementTexts []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		response.Header().Set("Content-Type", "application/json")
		switch method {
		case "sendMessage":
			chatID := request.FormValue("chat_id")
			if chatID == "-1001" {
				mu.Lock()
				groupTexts = append(groupTexts, request.FormValue("text"))
				mu.Unlock()
			} else if chatID == "-2001" {
				if request.FormValue("message_thread_id") != "77" {
					t.Errorf("management message thread = %q", request.FormValue("message_thread_id"))
				}
				mu.Lock()
				managementTexts = append(managementTexts, request.FormValue("text"))
				mu.Unlock()
			}
			_, _ = response.Write([]byte(`{"ok":true,"result":{"message_id":501,"date":1,"chat":{"id":-1001,"type":"supergroup"},"text":"ok"}}`))
		case "createChatInviteLink":
			createCalls.Add(1)
			if request.FormValue("creates_join_request") != "true" || request.FormValue("member_limit") != "" {
				t.Errorf("create form = %v", request.Form)
			}
			_, _ = response.Write([]byte(`{"ok":true,"result":{"invite_link":"` + secretURL + `","creator":{"id":123,"is_bot":true,"first_name":"TAdmin"},"creates_join_request":true,"is_primary":false,"is_revoked":false}}`))
		case "revokeChatInviteLink":
			revokeCalls.Add(1)
			_, _ = response.Write([]byte(`{"ok":true,"result":{"invite_link":"` + secretURL + `","creator":{"id":123,"is_bot":true,"first_name":"TAdmin"},"creates_join_request":true,"is_primary":false,"is_revoked":true}}`))
		default:
			http.Error(response, "unexpected method", http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "invite-commands.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := database.SaveManagedChatSetup(ctx, store.ManagedChat{
		ChatID: -1001, Title: "Source", Enabled: true, Language: "en", ManagementTopicID: 77,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 2592000,
	}, now); err != nil {
		t.Fatal(err)
	}
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	adapter := &gateway{bot: bot, token: "123:test"}
	invitationManager, err := invite.New(database, adapter, slog.New(slog.NewTextHandler(io.Discard, nil)), invite.Options{
		OwnerUserID: 1, BotToken: "123:test",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{OwnerUserID: 1, ManagementChatID: -2001, DefaultLanguage: "en", BotToken: "123:test"}
	handler := newCommandHandler(cfg, database, slog.New(slog.NewTextHandler(io.Discard, nil)),
		BotInfo{ID: 123, Username: "TAdminBot"}, "test", now, nil, nil, invitationManager)

	command := func(id int, actor int64, text string, senderChat *models.Chat) {
		handler.handle(ctx, bot, &models.Update{Message: &models.Message{
			ID: id, Date: int(now.Unix()), Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup},
			From: &models.User{ID: actor}, SenderChat: senderChat, Text: text,
			Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: 7}},
		}})
	}
	command(1, 2, "/tadmin invite create Denied", nil)
	command(2, 1087968824, "/tadmin invite create Anonymous", &models.Chat{ID: -1001, Type: models.ChatTypeSupergroup})
	if createCalls.Load() != 0 {
		t.Fatalf("unauthorized create calls = %d", createCalls.Load())
	}
	command(3, 1, "/tadmin invite create Friends", nil)
	if createCalls.Load() != 1 {
		t.Fatalf("owner create calls = %d", createCalls.Load())
	}
	summaries, err := database.ListInviteLinks(ctx, -1001, 10)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("invite summaries = %+v, %v", summaries, err)
	}
	command(4, 1, "/tadmin invite list", nil)
	command(5, 1, "/tadmin invite revoke "+summaries[0].PublicID, nil)
	command(6, 1, "/tadmin invite revoke "+summaries[0].PublicID, nil)
	if revokeCalls.Load() != 1 {
		t.Fatalf("idempotent revoke calls = %d", revokeCalls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	for _, text := range append(groupTexts, managementTexts...) {
		if strings.Contains(text, secretURL) || strings.Contains(text, "command-secret") {
			t.Fatalf("public summary leaked invite URL: %q", text)
		}
	}
	if len(managementTexts) == 0 {
		t.Fatal("invite events were not copied to the managed group's management topic")
	}
}

func TestModerationCommandsAuthorizeOwnerCurrentAdminAndRejectAnonymous(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		response.Header().Set("Content-Type", "application/json")
		switch method {
		case "getChatMember":
			userID := request.FormValue("user_id")
			status := "member"
			if userID == "2" {
				status = "administrator"
			}
			_, _ = response.Write([]byte(`{"ok":true,"result":{"status":"` + status + `","user":{"id":` + userID + `,"is_bot":false,"first_name":"Actor"},"can_be_edited":true,"is_anonymous":false,"can_manage_chat":true,"can_delete_messages":true,"can_restrict_members":true}}`))
		case "sendMessage":
			_, _ = response.Write([]byte(`{"ok":true,"result":{"message_id":501,"date":1,"chat":{"id":-1001,"type":"supergroup"},"text":"ok"}}`))
		default:
			http.Error(response, "unexpected method", http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "commands.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := database.SaveManagedChatSetup(ctx, store.ManagedChat{
		ChatID: -1001, Title: "Source", Enabled: true, Language: "en", ManagementTopicID: 77,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 2592000,
	}, now); err != nil {
		t.Fatal(err)
	}
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	adapter := &gateway{bot: bot, token: "123:test"}
	moderator := moderation.New(database, adapter, slog.New(slog.NewTextHandler(io.Discard, nil)), moderation.Options{
		OwnerUserID: 1, BotUserID: 123, ManagementChatID: -2001, BotToken: "123:test",
	})
	cfg := config.Config{OwnerUserID: 1, DefaultLanguage: "en", BotToken: "123:test"}
	handler := newCommandHandler(cfg, database, slog.New(slog.NewTextHandler(io.Discard, nil)), BotInfo{ID: 123, Username: "TAdminBot"}, "test", now, nil, moderator, nil)

	command := func(id int, actor int64, text string, senderChat *models.Chat) {
		handler.handle(ctx, bot, &models.Update{Message: &models.Message{
			ID: id, Date: int(now.Unix()), Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup},
			From: &models.User{ID: actor}, SenderChat: senderChat, Text: text,
			Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: 7}},
		}})
	}

	command(10, 2, "/tadmin mute 10m 42", nil)
	if _, err := database.GetModerationActionByKey(ctx, "manual:-1001:10:mute:42"); err != nil {
		t.Fatalf("administrator manual action: %v", err)
	}
	command(11, 3, "/tadmin ban 43", nil)
	if _, err := database.GetModerationActionByKey(ctx, "manual:-1001:11:ban:43"); !errors.Is(err, store.ErrModerationAction) {
		t.Fatalf("non-admin action = %v", err)
	}
	command(12, 1087968824, "/tadmin ban 44", &models.Chat{ID: -1001, Type: models.ChatTypeSupergroup})
	if _, err := database.GetModerationActionByKey(ctx, "manual:-1001:12:ban:44"); !errors.Is(err, store.ErrModerationAction) {
		t.Fatalf("anonymous action = %v", err)
	}

	command(13, 2, "/tadmin block add 45", nil)
	blocked, err := database.IsBlocked(ctx, -1001, 45)
	if err != nil || blocked {
		t.Fatalf("administrator changed owner-only blocklist: blocked=%t err=%v", blocked, err)
	}
	command(14, 1, "/tadmin block add 45", nil)
	blocked, err = database.IsBlocked(ctx, -1001, 45)
	if err != nil || !blocked {
		t.Fatalf("owner blocklist: blocked=%t err=%v", blocked, err)
	}
	command(15, 2, "/tadmin raid on", nil)
	status, err := database.GetModerationStatus(ctx, -1001, now)
	if err != nil || status.Mode != store.RaidModeAuto {
		t.Fatalf("administrator changed Raid: %+v err=%v", status, err)
	}
	command(16, 1, "/tadmin raid on", nil)
	status, err = database.GetModerationStatus(ctx, -1001, now)
	if err != nil || status.Mode != store.RaidModeForcedOn || !status.Active {
		t.Fatalf("owner Raid mode: %+v err=%v", status, err)
	}
}
