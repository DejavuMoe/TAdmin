package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newInviteStore(t *testing.T) (*Store, time.Time) {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "invite-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := database.SaveManagedChatSetup(context.Background(), ManagedChat{
		ChatID: -1001, Title: "Source", Enabled: true, Language: "en", ManagementTopicID: 77,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 2592000,
	}, now); err != nil {
		t.Fatal(err)
	}
	return database, now
}

func createInviteRecord(t *testing.T, database *Store, publicID, operationKey string, now time.Time) InviteLink {
	t.Helper()
	record, created, err := database.CreateInviteLinkRecord(context.Background(), InviteLink{
		PublicID: publicID, CreateOperationKey: operationKey, ChatID: -1001,
		DisplayName: "Test", TelegramName: "TAdmin " + publicID, CreatorUserID: 1,
	}, now)
	if err != nil || !created {
		t.Fatalf("CreateInviteLinkRecord() = %+v, %t, %v", record, created, err)
	}
	return record
}

func TestInviteCreateAndRevocationClaimsAreAtomic(t *testing.T) {
	ctx := context.Background()
	database, now := newInviteStore(t)
	record := createInviteRecord(t, database, "abcdefghijklmnop", "create-one", now)

	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := database.ClaimInviteCreate(ctx, record.PublicID, now)
			errorsChannel <- err
		}()
	}
	successes := 0
	for range 2 {
		err := <-errorsChannel
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInviteState) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("create claim successes = %d", successes)
	}
	if err := database.ActivateInviteLink(ctx, record.PublicID, []byte("ciphertext"), []byte("nonce"), []byte("fingerprint"), now); err != nil {
		t.Fatal(err)
	}
	requested, changed, err := database.RequestInviteRevocation(ctx, -1001, record.PublicID,
		"revoke-one", 1, now)
	if err != nil || !changed {
		t.Fatalf("RequestInviteRevocation() = %+v, %t, %v", requested, changed, err)
	}
	claims := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := database.ClaimInviteRevocation(ctx, record.PublicID, now.Add(-time.Minute), now, 5)
			claims <- err
		}()
	}
	successes = 0
	for range 2 {
		err := <-claims
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInviteState) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("revoke claim successes = %d", successes)
	}
}

func TestStaleInviteCreatesSettleWithoutReplay(t *testing.T) {
	ctx := context.Background()
	database, now := newInviteStore(t)
	pending := createInviteRecord(t, database, "abcdefghijklmnop", "pending-create", now)
	processing := createInviteRecord(t, database, "ponmlkjihgfedcba", "processing-create", now)
	if _, err := database.ClaimInviteCreate(ctx, processing.PublicID, now); err != nil {
		t.Fatal(err)
	}
	if err := database.SettleStaleInviteCreates(ctx, now.Add(5*time.Second), now.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{pending.PublicID: InviteFailed, processing.PublicID: InviteIndeterminate}
	for publicID, status := range want {
		record, err := database.GetInviteLink(ctx, -1001, publicID)
		if err != nil || record.Status != status || record.ActionState != InviteActionCompleted {
			t.Fatalf("settled %s = %+v, %v", publicID, record, err)
		}
	}
	if _, err := database.ClaimInviteCreate(ctx, processing.PublicID, now.Add(time.Minute)); !errors.Is(err, ErrInviteState) {
		t.Fatalf("stale processing create was replayable: %v", err)
	}
	var audits int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM audit_events WHERE event_type = 'invite_create_recovered'
	`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 2 {
		t.Fatalf("recovery audits = %d", audits)
	}
}

func TestUnconfirmedInviteDeliveryIsRecoveredByRevocation(t *testing.T) {
	ctx := context.Background()
	database, now := newInviteStore(t)
	record := createInviteRecord(t, database, "abcdefghijklmnop", "delivery-unknown", now)
	if _, err := database.ClaimInviteCreate(ctx, record.PublicID, now); err != nil {
		t.Fatal(err)
	}
	if err := database.ActivateInviteLink(ctx, record.PublicID,
		[]byte("ciphertext"), []byte("nonce"), []byte("fingerprint"), now); err != nil {
		t.Fatal(err)
	}
	if err := database.SettleStaleInviteCreates(ctx, now.Add(5*time.Second), now.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	recovered, err := database.GetInviteLink(ctx, -1001, record.PublicID)
	if err != nil || recovered.Status != InviteRevocationPending ||
		recovered.ActionKind != InviteActionRevoke || recovered.ActionState != InviteActionPending ||
		recovered.LastResult != "delivery_result_unknown" {
		t.Fatalf("recovered delivery = %+v, %v", recovered, err)
	}
	claimed, err := database.ClaimInviteRevocation(ctx, record.PublicID, now, now.Add(20*time.Second), 5)
	if err != nil || claimed.Attempts != 1 {
		t.Fatalf("revoke claim = %+v, %v", claimed, err)
	}
}

func TestDuplicateInviteOperationReturnsOriginalRecord(t *testing.T) {
	database, now := newInviteStore(t)
	first := createInviteRecord(t, database, "abcdefghijklmnop", "same-operation", now)
	second, created, err := database.CreateInviteLinkRecord(context.Background(), InviteLink{
		PublicID: "ponmlkjihgfedcba", CreateOperationKey: "same-operation", ChatID: -1001,
		DisplayName: "Other", TelegramName: "TAdmin ponmlkjihgfedcba", CreatorUserID: 1,
	}, now)
	if err != nil || created || second.ID != first.ID || second.PublicID != first.PublicID {
		t.Fatalf("duplicate = %+v, %t, %v; first=%+v", second, created, err, first)
	}
	for _, value := range []string{"https://t.me/+plaintext", "bot-token", "invite-answer"} {
		var found int
		if err := database.db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM audit_events WHERE metadata_json LIKE ?`, "%"+value+"%",
		).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found != 0 {
			t.Fatalf("audit contains %q", value)
		}
	}
}
