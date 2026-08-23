package invite

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tadmin/internal/store"
)

type fakeTelegram struct {
	mu             sync.Mutex
	preflightErr   error
	createErr      error
	deliveryErr    error
	revokeFailures int
	link           TelegramLink
	preflights     int
	creates        int
	deliveries     int
	revokes        int
	createNames    []string
}

func (f *fakeTelegram) SendInvitePreflight(context.Context, int64, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.preflights++
	return f.preflightErr
}

func (f *fakeTelegram) CreateJoinRequestInvite(_ context.Context, _ int64, name string) (TelegramLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	f.createNames = append(f.createNames, name)
	return f.link, f.createErr
}

func (f *fakeTelegram) SendInviteSecret(context.Context, int64, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliveries++
	return f.deliveryErr
}

func (f *fakeTelegram) RevokeInvite(context.Context, int64, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokes++
	if f.revokeFailures > 0 {
		f.revokeFailures--
		return errors.New("Telegram revoke failed with https://t.me/+must-not-leak")
	}
	return nil
}

func (f *fakeTelegram) counts() (preflight, creates, deliveries, revokes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.preflights, f.creates, f.deliveries, f.revokes
}

func newManagerTest(t *testing.T, token string, fake *fakeTelegram) (*Manager, *store.Store, string, *bytes.Buffer, time.Time) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "invites.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := database.SaveManagedChatSetup(ctx, store.ManagedChat{
		ChatID: -1001, Title: "Source", Enabled: true, Language: "en", ManagementTopicID: 77,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 2592000,
	}, now); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	manager, err := New(database, fake, slog.New(slog.NewTextHandler(&logs, nil)), Options{
		OwnerUserID: 1, BotToken: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return now }
	return manager, database, path, &logs, now
}

func TestCreateInviteEncryptsSecretAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	const secretURL = "https://t.me/+private-invite-value"
	fake := &fakeTelegram{link: TelegramLink{URL: secretURL, CreatesJoinRequest: true}}
	manager, database, path, logs, _ := newManagerTest(t, "123:test-token", fake)

	result, err := manager.Create(ctx, "command:-1001:10", -1001, 1, "Friends 2026", "en")
	if err != nil || result.Code != ResultDelivered || !validPublicID(result.PublicID) {
		t.Fatalf("Create() = %+v, %v", result, err)
	}
	record, err := database.GetInviteLink(ctx, -1001, result.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != store.InviteActive || len(record.EncryptedLink) == 0 ||
		bytes.Contains(record.EncryptedLink, []byte("private-invite-value")) {
		t.Fatalf("stored invite = %+v", record)
	}
	preflight, creates, deliveries, revokes := fake.counts()
	if preflight != 1 || creates != 1 || deliveries != 1 || revokes != 0 ||
		len(fake.createNames) != 1 || fake.createNames[0] != "TAdmin "+result.PublicID {
		t.Fatalf("Telegram calls = preflight:%d create:%d delivery:%d revoke:%d names:%v",
			preflight, creates, deliveries, revokes, fake.createNames)
	}

	duplicate, err := manager.Create(ctx, "command:-1001:10", -1001, 1, "Different Name", "en")
	if err != nil || duplicate.Code != ResultExisting || duplicate.PublicID != result.PublicID {
		t.Fatalf("duplicate Create() = %+v, %v", duplicate, err)
	}
	_, creates, deliveries, _ = fake.counts()
	if creates != 1 || deliveries != 1 {
		t.Fatalf("duplicate external calls create=%d delivery=%d", creates, deliveries)
	}
	if strings.Contains(logs.String(), secretURL) {
		t.Fatalf("log contains invite URL: %s", logs.String())
	}
	if _, err := manager.List(ctx, -1001, 2); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("non-owner list error = %v", err)
	}
	if _, err := manager.Create(ctx, "command:-1001:11", -1001, 2, "Denied", "en"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("non-owner create error = %v", err)
	}
	if err := database.SaveManagedChatSetup(ctx, store.ManagedChat{
		ChatID: -1002, Title: "Other", Enabled: true, Language: "en", ManagementTopicID: 78,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 2592000,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Revoke(ctx, "command:-1002:12", -1002, 1, result.PublicID); !errors.Is(err, store.ErrInviteNotFound) {
		t.Fatalf("cross-group revoke error = %v", err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + "-wal"} {
		contents, err := os.ReadFile(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		if bytes.Contains(contents, []byte(secretURL)) || bytes.Contains(contents, []byte("private-invite-value")) {
			t.Fatalf("database file %s contains the plaintext invite", candidate)
		}
	}
}

func TestCreateInvitePreflightFailureAndUnknownResultAreNotReplayed(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTelegram{
		link:         TelegramLink{URL: "https://t.me/+unused", CreatesJoinRequest: true},
		preflightErr: errors.New("blocked https://t.me/+must-not-leak"),
	}
	manager, database, _, logs, now := newManagerTest(t, "123:test-token", fake)
	result, err := manager.Create(ctx, "command:-1001:20", -1001, 1, "Preflight", "en")
	if err != nil || result.Code != ResultPreflightFailed {
		t.Fatalf("preflight Create() = %+v, %v", result, err)
	}
	record, err := database.GetInviteLink(ctx, -1001, result.PublicID)
	if err != nil || record.Status != store.InviteFailed {
		t.Fatalf("preflight record = %+v, %v", record, err)
	}
	_, creates, _, _ := fake.counts()
	if creates != 0 {
		t.Fatalf("create calls after preflight failure = %d", creates)
	}

	fake.preflightErr = nil
	fake.createErr = errors.New("timeout https://t.me/+must-not-leak")
	unknown, err := manager.Create(ctx, "command:-1001:21", -1001, 1, "Unknown", "en")
	if err != nil || unknown.Code != ResultCreateIndeterminate {
		t.Fatalf("unknown Create() = %+v, %v", unknown, err)
	}
	manager.now = func() time.Time { return now.Add(time.Minute) }
	manager.reconcile(ctx)
	_, creates, _, _ = fake.counts()
	if creates != 1 {
		t.Fatalf("indeterminate create was replayed: %d calls", creates)
	}

	fake.createErr = ErrTelegramPermission
	fake.link = TelegramLink{}
	denied, err := manager.Create(ctx, "command:-1001:22", -1001, 1, "Denied", "en")
	if err != nil || denied.Code != ResultPermissionDenied {
		t.Fatalf("permission Create() = %+v, %v", denied, err)
	}
	record, err = database.GetInviteLink(ctx, -1001, denied.PublicID)
	if err != nil || record.Status != store.InviteFailed {
		t.Fatalf("permission record = %+v, %v", record, err)
	}
	manager.now = func() time.Time { return now.Add(2 * time.Minute) }
	manager.reconcile(ctx)
	_, creates, _, _ = fake.counts()
	if creates != 2 {
		t.Fatalf("rejected create was replayed: %d calls", creates)
	}
	if strings.Contains(logs.String(), "must-not-leak") || strings.Contains(logs.String(), "t.me") {
		t.Fatalf("log leaked external error: %s", logs.String())
	}
}

func TestDeliveryFailureRevokesInvite(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTelegram{
		link:        TelegramLink{URL: "https://t.me/+delivery-failure", CreatesJoinRequest: true},
		deliveryErr: errors.New("owner blocked bot"),
	}
	manager, database, _, _, _ := newManagerTest(t, "123:test-token", fake)
	result, err := manager.Create(ctx, "command:-1001:30", -1001, 1, "Delivery", "en")
	if err != nil || result.Code != ResultDeliveryFailedRevoked {
		t.Fatalf("Create() = %+v, %v", result, err)
	}
	record, err := database.GetInviteLink(ctx, -1001, result.PublicID)
	if err != nil || record.Status != store.InviteRevoked || len(record.EncryptedLink) != 0 || len(record.LinkNonce) != 0 {
		t.Fatalf("revoked record = %+v, %v", record, err)
	}
	_, _, _, revokes := fake.counts()
	if revokes != 1 {
		t.Fatalf("revoke calls = %d", revokes)
	}
}

func TestRevokeRetriesAndTokenRotationFailsClosed(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTelegram{link: TelegramLink{URL: "https://t.me/+retry-secret", CreatesJoinRequest: true}}
	manager, database, _, _, now := newManagerTest(t, "123:original-token", fake)
	created, err := manager.Create(ctx, "command:-1001:40", -1001, 1, "Retry", "en")
	if err != nil || created.Code != ResultDelivered {
		t.Fatalf("Create() = %+v, %v", created, err)
	}

	wrongToken, err := New(database, fake, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		OwnerUserID: 1, BotToken: "123:rotated-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongToken.now = func() time.Time { return now.Add(time.Second) }
	result, err := wrongToken.Revoke(ctx, "command:-1001:41", -1001, 1, created.PublicID)
	if err != nil || result.Code != ResultDecryptFailed {
		t.Fatalf("wrong-token Revoke() = %+v, %v", result, err)
	}
	_, _, _, revokes := fake.counts()
	if revokes != 0 {
		t.Fatalf("Telegram revoke called with undecryptable data: %d", revokes)
	}

	manager.now = func() time.Time { return now.Add(2 * time.Second) }
	recovered, err := manager.Revoke(ctx, "command:-1001:42", -1001, 1, created.PublicID)
	if err != nil || recovered.Code != ResultRevoked {
		t.Fatalf("restored-token Revoke() = %+v, %v", recovered, err)
	}
	repeated, err := manager.Revoke(ctx, "command:-1001:43", -1001, 1, created.PublicID)
	if err != nil || repeated.Code != ResultAlreadyRevoked {
		t.Fatalf("repeat Revoke() = %+v, %v", repeated, err)
	}
	_, _, _, revokes = fake.counts()
	if revokes != 1 {
		t.Fatalf("idempotent revoke calls = %d", revokes)
	}
}

func TestFailedRevocationRecoversAfterRestart(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTelegram{
		link: TelegramLink{URL: "https://t.me/+recovery-secret", CreatesJoinRequest: true}, revokeFailures: 1,
	}
	manager, database, _, _, now := newManagerTest(t, "123:test-token", fake)
	created, err := manager.Create(ctx, "command:-1001:50", -1001, 1, "Recovery", "en")
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Revoke(ctx, "command:-1001:51", -1001, 1, created.PublicID)
	if err != nil || result.Code != ResultRevocationPending {
		t.Fatalf("initial Revoke() = %+v, %v", result, err)
	}
	restarted, err := New(database, fake, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		OwnerUserID: 1, BotToken: "123:test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return now.Add(20 * time.Second) }
	restarted.reconcile(ctx)
	record, err := database.GetInviteLink(ctx, -1001, created.PublicID)
	if err != nil || record.Status != store.InviteRevoked || record.Attempts != 2 {
		t.Fatalf("recovered record = %+v, %v", record, err)
	}
}
