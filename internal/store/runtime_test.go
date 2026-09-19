package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestInstanceLockAndPollingOffsetSurviveRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Open(ctx, path); err == nil {
		second.Close()
		t.Fatal("two instances acquired database")
	}
	if err := db.SavePollOffset(ctx, 321); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	offset, err := db.PollOffset(ctx)
	if err != nil || offset != 321 {
		t.Fatalf("offset=%d err=%v", offset, err)
	}
}
func TestAdmissionProofMustBeApprovedAndConsumedOnce(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.SaveManagedChatSetup(ctx, ManagedChat{ChatID: -1, Title: "Test", Language: "en", Enabled: true, VerificationTimeoutSeconds: 180}, now); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"decline_request", "approve_request"} {
		item, _, err := db.CreateJoinCase(ctx, JoinCase{SourceKey: kind, SourceRef: kind, Path: JoinPathRequest, ChatID: -1, UserID: 42}, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.ScheduleCaseAction(ctx, item.ID, kind, now); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ClaimCaseAction(ctx, item.ID, now.Add(-time.Minute), now); err != nil {
			t.Fatal(err)
		}
		if err := db.CompleteCaseAction(ctx, item.ID, kind, JoinResolved, 0, false, now); err != nil {
			t.Fatal(err)
		}
		direct, _, err := db.CreateJoinCase(ctx, JoinCase{SourceKey: kind + "-direct", SourceRef: kind + "-direct", Path: JoinPathDirect, ChatID: -1, UserID: 42}, now)
		if err != nil {
			t.Fatal(err)
		}
		got, err := db.UseResolvedApplication(ctx, direct.ID, now.Add(-time.Minute), now)
		if err != nil || got != (kind == "approve_request") {
			t.Fatalf("%s proof=%t err=%v", kind, got, err)
		}
		if got {
			saved, _ := db.GetJoinCase(ctx, direct.ID)
			if saved.ActionKind != "record_member" || saved.ActionState != ActionPending {
				t.Fatal("proof and action not atomic")
			}
			if again, err := db.UseResolvedApplication(ctx, direct.ID, now.Add(-time.Minute), now); err != nil || !again {
				t.Fatal("lost proof on replay", err)
			}
		}
		second, _, err := db.CreateJoinCase(ctx, JoinCase{SourceKey: kind + "-next", SourceRef: kind + "-next", Path: JoinPathDirect, ChatID: -1, UserID: 42}, now)
		if err != nil {
			t.Fatal(err)
		}
		if twice, err := db.UseResolvedApplication(ctx, second.ID, now.Add(-time.Minute), now); err != nil || twice {
			t.Fatal("proof reusable across joins", err)
		}

	}
}

func TestDatabaseIdentitySurvivesTokenRotationButRejectsAnotherBot(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.BindBot(t.Context(), 123); err != nil {
		t.Fatal(err)
	}
	if err := db.BindBot(t.Context(), 123); err != nil {
		t.Fatal("same bot rejected", err)
	}
	if err := db.BindBot(t.Context(), 456); err == nil {
		t.Fatal("different bot can replay stored actions")
	}
}
