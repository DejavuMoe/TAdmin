package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestVerificationStateTransitions(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "tadmin.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := database.SaveManagedChat(ctx, -1001, "Test", "zh-CN", true, now); err != nil {
		t.Fatal(err)
	}

	session := VerificationSession{
		ID:              "session-state-test",
		ChatID:          -1001,
		UserID:          42,
		SourceKind:      "join_request",
		SourceRef:       "opaque",
		Language:        "zh-CN",
		Status:          VerificationChallengeIssued,
		Generation:      1,
		ChallengeSecret: bytes.Repeat([]byte{1}, 32),
		ExpiresAt:       now.Add(3 * time.Minute),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := database.CreateVerificationSession(ctx, session); err != nil {
		t.Fatalf("CreateVerificationSession() error = %v", err)
	}
	if _, err := database.SubmitVerification(ctx, session.ID, 9, false, bytes.Repeat([]byte{2}, 32), 3, now); !errors.Is(err, ErrVerificationStale) {
		t.Fatalf("SubmitVerification(stale) error = %v", err)
	}
	result, err := database.SubmitVerification(ctx, session.ID, 1, true, nil, 3, now)
	if err != nil {
		t.Fatalf("SubmitVerification(correct) error = %v", err)
	}
	if result.Session.Status != VerificationPassed || result.Session.ConsumedAt == nil {
		t.Fatalf("successful transition = %+v", result.Session)
	}
	if _, err := database.SubmitVerification(ctx, session.ID, 1, true, nil, 3, now); !errors.Is(err, ErrVerificationCompleted) {
		t.Fatalf("SubmitVerification(completed) error = %v", err)
	}
	if err := database.CancelVerificationSession(ctx, session.ID, now); !errors.Is(err, ErrVerificationCompleted) {
		t.Fatalf("CancelVerificationSession(completed) error = %v", err)
	}
}

func TestVerificationCancelAndInvalidCreation(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "tadmin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := database.SaveManagedChat(ctx, -1001, "Test", "zh-CN", true, now); err != nil {
		t.Fatal(err)
	}
	invalid := VerificationSession{Status: VerificationPending, Generation: 0}
	if err := database.CreateVerificationSession(ctx, invalid); !errors.Is(err, ErrVerificationInvalidState) {
		t.Fatalf("CreateVerificationSession(invalid) error = %v", err)
	}
	valid := VerificationSession{
		ID: "cancel-test", ChatID: -1001, UserID: 42, SourceKind: "join_request", SourceRef: "opaque",
		Language: "en", Status: VerificationChallengeIssued, Generation: 1,
		ChallengeSecret: bytes.Repeat([]byte{3}, 32), ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateVerificationSession(ctx, valid); err != nil {
		t.Fatal(err)
	}
	if err := database.CancelVerificationSession(ctx, valid.ID, now); err != nil {
		t.Fatalf("CancelVerificationSession() error = %v", err)
	}
	if err := database.CancelVerificationSession(ctx, valid.ID, now); !errors.Is(err, ErrVerificationCancelled) {
		t.Fatalf("second CancelVerificationSession() error = %v", err)
	}
}
