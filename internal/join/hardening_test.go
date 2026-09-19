package join

import (
	"errors"
	"tadmin/internal/store"
	"testing"
	"time"
)

func TestDirectExpiryRemovesImmediatelyAndNeverBans(t *testing.T) {
	f := newJoinFixture(t)
	if err := f.joins.HandleMemberUpdate(f.ctx, directUpdate(201, 201)); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathDirect, -1001, "201")
	f.joins.now = func() time.Time { return item.DeadlineAt }
	f.joins.reconcileAll(f.ctx)
	got, _ := f.database.GetJoinCase(f.ctx, item.ID)
	if got.Status != store.JoinResolved || f.telegram.count(ActionKick) != 1 || f.telegram.count(ActionBan) != 0 || f.telegram.count(ActionRestore) != 0 {
		t.Fatalf("case=%+v calls=%v", got, f.telegram.calls)
	}
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count(ActionKick) != 1 {
		t.Fatal("duplicate removal")
	}
}

func TestRestrictionFailureDoesNotExtendAdmissionDeadline(t *testing.T) {
	f := newJoinFixture(t)
	f.telegram.fail[ActionRestrict] = errors.New("network unavailable")
	_ = f.joins.HandleMemberUpdate(f.ctx, directUpdate(202, 202))
	item := mustCase(t, f, store.JoinPathDirect, -1001, "202")
	if item.VerificationSessionID != "" {
		t.Fatal("verification offered before successful restriction")
	}
	delete(f.telegram.fail, ActionRestrict)
	f.joins.now = func() time.Time { return item.DeadlineAt.Add(time.Second) }
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count(ActionDirectPrompt) != 0 || f.telegram.count(ActionKick) != 1 {
		t.Fatalf("late initialization: %v", f.telegram.calls)
	}
}

func TestPassedBeforeDeadlineRestoresAfterOfflineRecovery(t *testing.T) {
	f := newJoinFixture(t)
	if err := f.joins.HandleMemberUpdate(f.ctx, directUpdate(203, 203)); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathDirect, -1001, "203")
	passSession(t, f, item.VerificationSessionID)
	f.joins.now = func() time.Time { return item.DeadlineAt.Add(time.Hour) }
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count(ActionRestore) != 1 || f.telegram.count(ActionKick) != 0 {
		t.Fatal(f.telegram.calls)
	}
}

func TestPendingMemberPromotedToAdminIsNotRemoved(t *testing.T) {
	f := newJoinFixture(t)
	if err := f.joins.HandleMemberUpdate(f.ctx, directUpdate(204, 204)); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathDirect, -1001, "204")
	f.telegram.admins[204] = true
	f.joins.now = func() time.Time { return item.DeadlineAt }
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count(ActionKick) != 0 {
		t.Fatal("removed promoted administrator")
	}
	got, _ := f.database.GetJoinCase(f.ctx, item.ID)
	if got.Status != store.JoinCancelled {
		t.Fatal(got)
	}
}

func TestReviewRequiresSavedNonzeroMessageAndChat(t *testing.T) {
	f := newJoinFixture(t)
	f.telegram.fail[ActionDirectPrompt] = errors.New("delivery failed")
	if err := f.joins.HandleMemberUpdate(f.ctx, directUpdate(205, 205)); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathDirect, -1001, "205")
	review, _ := f.database.GetReviewByCase(f.ctx, item.ID)
	callback := ReviewCallback{ID: "test", ActorUserID: 1, ManagementChat: -2001, MessageID: 999, Data: "ta:r:" + review.PublicID + ":o"}
	if err := f.joins.HandleReviewCallback(f.ctx, callback); err != nil {
		t.Fatal(err)
	}
	if f.telegram.count(ActionRestore) != 0 {
		t.Fatal("unsaved card authorized")
	}
	f.joins.reconcileAll(f.ctx)
	review, _ = f.database.GetReviewByCase(f.ctx, item.ID)
	callback.MessageID = review.ManagementMessageID
	callback.ManagementChat = -9999
	if err := f.joins.HandleReviewCallback(f.ctx, callback); err != nil {
		t.Fatal(err)
	}
	if f.telegram.count(ActionRestore) != 0 {
		t.Fatal("wrong chat authorized")
	}
	// Configuration changes never change the saved card's authority/location.
	f.joins.options.ManagementChatID = -9999
	callback.ManagementChat = -2001
	if err := f.joins.HandleReviewCallback(f.ctx, callback); err != nil {
		t.Fatal(err)
	}
	if f.telegram.count(ActionRestore) != 1 {
		t.Fatal(f.telegram.calls)
	}
}

func TestFailedRemovalRetriesAfterDelay(t *testing.T) {
	f := newJoinFixture(t)
	if err := f.joins.HandleMemberUpdate(f.ctx, directUpdate(206, 206)); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathDirect, -1001, "206")
	now := item.DeadlineAt
	f.joins.now = func() time.Time { return now }
	f.telegram.fail[ActionKick] = errors.New("unavailable")
	f.joins.reconcileAll(f.ctx)
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count(ActionKick) != 1 {
		t.Fatal("failed action retried without backoff")
	}
	delete(f.telegram.fail, ActionKick)
	now = now.Add(actionRetryAge)
	f.joins.reconcileAll(f.ctx)
	got, _ := f.database.GetJoinCase(f.ctx, item.ID)
	if f.telegram.count(ActionKick) != 2 || got.Status != store.JoinResolved {
		t.Fatalf("case=%+v calls=%v", got, f.telegram.calls)
	}
}

func TestPriorMemberRestrictionsArePreservedOnSuccess(t *testing.T) {
	f := newJoinFixture(t)
	update := directUpdate(207, 207)
	update.PriorPermissions = `{"can_send_messages":false,"can_send_photos":false}`
	if err := f.joins.HandleMemberUpdate(f.ctx, update); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathDirect, -1001, "207")
	if item.PriorPermissions != update.PriorPermissions {
		t.Fatal("lost restriction snapshot")
	}
	passSession(t, f, item.VerificationSessionID)
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count("restore_prior") != 1 || f.telegram.count(ActionRestore) != 0 {
		t.Fatal(f.telegram.calls)
	}
}

func TestExternallyHandledRequestClosesWithoutFalseApproval(t *testing.T) {
	f := newJoinFixture(t)
	if err := f.joins.HandleJoinRequest(f.ctx, ordinaryRequest(208, 208, 900208)); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathRequest, -1001, "208")
	f.telegram.fail[ActionDecline] = ErrRequestHandled
	f.joins.now = func() time.Time { return item.DeadlineAt }
	f.joins.reconcileAll(f.ctx)
	f.joins.reconcileAll(f.ctx)
	got, _ := f.database.GetJoinCase(f.ctx, item.ID)
	if got.Status != store.JoinCancelled || f.telegram.count(ActionDecline) != 1 || f.telegram.count(ActionApprove) != 0 {
		t.Fatalf("case=%+v calls=%v", got, f.telegram.calls)
	}
}
