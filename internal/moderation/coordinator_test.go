package moderation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"tadmin/internal/store"
)

type fakeTelegram struct {
	mu       sync.Mutex
	admins   map[int64]bool
	failNext map[string]error
	calls    []string
	until    []time.Time
	notices  []string
	logs     []string
}

func (f *fakeTelegram) record(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	if err := f.failNext[name]; err != nil {
		delete(f.failNext, name)
		return err
	}
	return nil
}

func (f *fakeTelegram) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.calls {
		if call == name {
			count++
		}
	}
	return count
}

func (f *fakeTelegram) DeleteModerationMessage(context.Context, int64, int) error {
	return f.record("delete")
}

func (f *fakeTelegram) SendModerationNotice(_ context.Context, _ int64, text string) error {
	f.mu.Lock()
	f.notices = append(f.notices, text)
	f.mu.Unlock()
	return f.record("notice")
}
func (f *fakeTelegram) RestrictMemberUntil(_ context.Context, _ int64, _ int64, until time.Time) error {
	f.mu.Lock()
	f.until = append(f.until, until)
	f.mu.Unlock()
	return f.record("mute")
}
func (f *fakeTelegram) KickMember(context.Context, int64, int64) error { return f.record("kick") }
func (f *fakeTelegram) BanMember(context.Context, int64, int64) error  { return f.record("ban") }
func (f *fakeTelegram) IsAdministrator(_ context.Context, _ int64, userID int64) (bool, error) {
	return f.admins[userID], f.record("admin_check")
}
func (f *fakeTelegram) SendManagementLog(_ context.Context, _ int64, _ int, text string) error {
	f.mu.Lock()
	f.logs = append(f.logs, text)
	f.mu.Unlock()
	return f.record("management_log")
}

func newCoordinatorTest(t *testing.T) (*Coordinator, *store.Store, *fakeTelegram, time.Time) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "coordinator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := database.SaveManagedChatSetup(ctx, store.ManagedChat{
		ChatID: -1001, Title: "Source", Enabled: true, Language: "en", ManagementTopicID: 77,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 2592000,
	}, now); err != nil {
		t.Fatal(err)
	}
	fake := &fakeTelegram{admins: map[int64]bool{}, failNext: map[string]error{}}
	coordinator := New(database, fake, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		OwnerUserID: 1, BotUserID: 999, ManagementChatID: -2001, BotToken: "123:test-token",
	})
	coordinator.now = func() time.Time { return now }
	return coordinator, database, fake, now
}

func recentUser(t *testing.T, database *store.Store, userID int64, now time.Time) {
	t.Helper()
	if err := database.UpsertMemberState(context.Background(), -1001, userID, now.Add(-time.Minute), now); err != nil {
		t.Fatal(err)
	}
}

func TestHandleMessageTextCaptionEditedAndSenderChatIdentity(t *testing.T) {
	ctx := context.Background()
	coordinator, database, _, now := newCoordinatorTest(t)
	for _, userID := range []int64{10, 11, 12} {
		recentUser(t, database, userID, now)
	}
	textMessage := &models.Message{
		ID: 1, Date: int(now.Unix()), Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 10}, Text: "visit https://t.me/promo_secret",
	}
	if err := coordinator.HandleMessage(ctx, 1, textMessage, false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetModerationActionByKey(ctx, "auto:-1001:user:10:3:1"); err != nil {
		t.Fatalf("text action: %v", err)
	}

	captionMessage := &models.Message{
		ID: 2, Date: int(now.Unix()), Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 11}, Caption: "https://t.me/caption_secret",
		Photo: []models.PhotoSize{{FileUniqueID: "photo-unique"}},
	}
	if err := coordinator.HandleMessage(ctx, 2, captionMessage, false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetModerationActionByKey(ctx, "auto:-1001:user:11:3:2"); err != nil {
		t.Fatalf("caption action: %v", err)
	}

	editedMessage := &models.Message{
		ID: 3, Date: int(now.Unix()), Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 12}, Text: "ordinary message",
	}
	if err := coordinator.HandleMessage(ctx, 3, editedMessage, false); err != nil {
		t.Fatal(err)
	}
	editedMessage.Text = "https://t.me/edited_secret"
	editedMessage.EditDate = int(now.Add(time.Second).Unix())
	coordinator.now = func() time.Time { return now.Add(time.Second) }
	if err := coordinator.HandleMessage(ctx, 4, editedMessage, true); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.HandleMessage(ctx, 4, editedMessage, true); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetModerationActionByKey(ctx, "auto:-1001:user:12:3:3"); err != nil {
		t.Fatalf("edited action: %v", err)
	}

	text, mentions := distinctMentions(10)
	senderMessage := &models.Message{
		ID: 4, Date: int(now.Unix()), Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 1087968824}, SenderChat: &models.Chat{ID: -3001, Type: models.ChatTypeChannel},
		Text: text, Entities: mentions,
	}
	if err := coordinator.HandleMessage(ctx, 5, senderMessage, false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetModerationActionByKey(ctx, "auto:-1001:sender_chat:-3001:3:4"); err != nil {
		t.Fatalf("sender_chat action: %v", err)
	}
	if _, err := database.GetModerationActionByKey(ctx, "auto:-1001:user:1087968824:3:4"); !errors.Is(err, store.ErrModerationAction) {
		t.Fatalf("message.From was treated as target: %v", err)
	}

	selfSender := *senderMessage
	selfSender.ID = 5
	selfSender.SenderChat = &models.Chat{ID: -1001, Type: models.ChatTypeSupergroup}
	if err := coordinator.HandleMessage(ctx, 6, &selfSender, false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetModerationActionByKey(ctx, "auto:-1001:sender_chat:-1001:3:5"); !errors.Is(err, store.ErrModerationAction) {
		t.Fatalf("self sender_chat action = %v", err)
	}
}

func TestCoordinatorExecutesActionsSkipsProtectedUsersAndLogsNoOriginalText(t *testing.T) {
	ctx := context.Background()
	coordinator, database, fake, now := newCoordinatorTest(t)
	recentUser(t, database, 20, now)
	message := &models.Message{
		ID: 20, Date: int(now.Unix()), Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 20}, Text: "buy now https://t.me/full-secret-token?invite=private",
	}
	if err := coordinator.HandleMessage(ctx, 20, message, false); err != nil {
		t.Fatal(err)
	}
	coordinator.reconcile(ctx)
	coordinator.reconcile(ctx)
	if fake.count("delete") != 1 || fake.count("notice") != 1 || fake.count("management_log") != 1 {
		t.Fatalf("calls = %+v", fake.calls)
	}
	fake.mu.Lock()
	notices := strings.Join(fake.notices, "\n")
	fake.mu.Unlock()
	if !strings.Contains(notices, "has been warned") {
		t.Fatalf("moderation notice = %q", notices)
	}
	fake.mu.Lock()
	logs := strings.Join(fake.logs, "\n")
	fake.mu.Unlock()
	for _, secret := range []string{"buy now", "full-secret-token", "invite=private", "https://"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("management log leaked %q: %s", secret, logs)
		}
	}
	if !strings.Contains(logs, "t.me") || !strings.Contains(logs, store.RuleTelegramPromotion) {
		t.Fatalf("sanitized management log = %s", logs)
	}

	fake.admins[21] = true
	adminMessage := &models.Message{
		ID: 21, Date: int(now.Unix()), Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 21},
	}
	adminMessage.Text, adminMessage.Entities = distinctMentions(10)
	if err := coordinator.HandleMessage(ctx, 21, adminMessage, false); err != nil {
		t.Fatal(err)
	}
	coordinator.reconcile(ctx)
	if fake.count("delete") != 1 {
		t.Fatalf("administrator was automatically deleted; calls=%+v", fake.calls)
	}
	action, err := database.GetModerationActionByKey(ctx, "auto:-1001:user:21:3:21")
	if err != nil || action.Result != "skipped_admin" {
		t.Fatalf("admin action = %+v, err=%v", action, err)
	}

	ownerMessage := *adminMessage
	ownerMessage.ID = 22
	ownerMessage.From = &models.User{ID: 1}
	if err := coordinator.HandleMessage(ctx, 22, &ownerMessage, false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetModerationActionByKey(ctx, "auto:-1001:user:1:3:22"); !errors.Is(err, store.ErrModerationAction) {
		t.Fatalf("owner action = %v", err)
	}

	if _, err := coordinator.UpdateList(ctx, "allow-22", -1001, 22, "allow", true, 1); err != nil {
		t.Fatal(err)
	}
	allowMessage := *adminMessage
	allowMessage.ID = 23
	allowMessage.From = &models.User{ID: 22}
	if err := coordinator.HandleMessage(ctx, 23, &allowMessage, false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetModerationActionByKey(ctx, "auto:-1001:user:22:3:23"); !errors.Is(err, store.ErrModerationAction) {
		t.Fatalf("allowlist action = %v", err)
	}
}

func TestCoordinatorMuteDeadlineAndFailedActionRestartRecovery(t *testing.T) {
	ctx := context.Background()
	coordinator, database, fake, now := newCoordinatorTest(t)
	created, err := coordinator.ScheduleManual(ctx, 30, -1001, 30, 1, store.ModerationMute, 10*time.Minute)
	if err != nil || !created {
		t.Fatalf("manual mute = %t, %v", created, err)
	}
	coordinator.reconcile(ctx)
	fake.mu.Lock()
	if len(fake.until) != 1 || !fake.until[0].Equal(now.Add(10*time.Minute)) {
		t.Fatalf("mute deadlines = %+v", fake.until)
	}
	fake.mu.Unlock()

	recentUser(t, database, 31, now)
	fake.failNext["delete"] = errors.New("temporary Telegram failure")
	message := &models.Message{
		ID: 31, Date: int(now.Unix()), Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 31}, Text: "https://t.me/retry",
	}
	if err := coordinator.HandleMessage(ctx, 31, message, false); err != nil {
		t.Fatal(err)
	}
	coordinator.reconcile(ctx)
	action, err := database.GetModerationActionByKey(ctx, "auto:-1001:user:31:3:31")
	if err != nil || action.Status != store.ModerationFailed {
		t.Fatalf("failed action = %+v, err=%v", action, err)
	}

	restarted := New(database, fake, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		OwnerUserID: 1, BotUserID: 999, ManagementChatID: -2001, BotToken: "123:test-token",
	})
	restarted.now = func() time.Time { return now.Add(20 * time.Second) }
	restarted.reconcile(ctx)
	action, err = database.GetModerationActionByKey(ctx, "auto:-1001:user:31:3:31")
	if err != nil || action.Status != store.ModerationCompleted || action.Attempts != 2 {
		t.Fatalf("recovered action = %+v, err=%v", action, err)
	}
}

func TestCoordinatorDoesNotResendStaleNotificationActionsAfterRestart(t *testing.T) {
	ctx := context.Background()
	coordinator, database, fake, now := newCoordinatorTest(t)
	created, err := coordinator.ScheduleManual(ctx, 40, -1001, 40, 1, store.ModerationMute, 10*time.Minute)
	if err != nil || !created {
		t.Fatalf("manual mute = %t, %v", created, err)
	}
	coordinator.reconcile(ctx)
	if fake.count("notice") != 0 || fake.count("management_log") != 0 {
		t.Fatalf("enforcement sent notification inline: %+v", fake.calls)
	}
	claimed, err := database.ClaimModerationActions(ctx, now.Add(-time.Minute), now.Add(time.Second), 10)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claimed notification actions = %+v, %v", claimed, err)
	}

	restarted := New(database, fake, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		OwnerUserID: 1, BotUserID: 999, ManagementChatID: -2001, BotToken: "123:test-token",
	})
	restarted.now = func() time.Time { return now.Add(20 * time.Second) }
	restarted.reconcile(ctx)
	if fake.count("notice") != 0 || fake.count("management_log") != 0 {
		t.Fatalf("stale notification was resent: %+v", fake.calls)
	}
	for _, key := range []string{"manual:-1001:40:mute:40:notify", "manual:-1001:40:mute:40:log"} {
		action, err := database.GetModerationActionByKey(ctx, key)
		if err != nil || action.Status != store.ModerationCompleted || action.Result != "indeterminate_after_restart" {
			t.Fatalf("settled action %s = %+v, %v", key, action, err)
		}
	}
}

func distinctMentions(count int) (string, []models.MessageEntity) {
	var builder strings.Builder
	entities := make([]models.MessageEntity, 0, count)
	for index := 0; index < count; index++ {
		value := "@" + string(rune('a'+index))
		offset := builder.Len()
		builder.WriteString(value)
		builder.WriteByte(' ')
		entities = append(entities, models.MessageEntity{
			Type: models.MessageEntityTypeMention, Offset: offset, Length: len(value),
		})
	}
	return builder.String(), entities
}
