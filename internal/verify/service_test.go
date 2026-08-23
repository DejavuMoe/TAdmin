package verify

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"tadmin/internal/store"
)

const testChatID int64 = -1001234567890

func TestOpenRejectsUserAndSourceMismatch(t *testing.T) {
	service, _, now := newTestService(t)
	token := createTestSession(t, service, 424242, "join_request", "opaque-request")

	_, err := service.Open(context.Background(), token, testInitData(t, now, 999999, token, ""))
	if !errors.Is(err, ErrSessionIdentity) {
		t.Fatalf("Open(other user) error = %v, want ErrSessionIdentity", err)
	}

	guardToken := createTestSession(t, service, 424242, SourceGuardQuery, "expected-query")
	_, err = service.Open(context.Background(), guardToken, testInitData(t, now, 424242, guardToken, "other-query"))
	if !errors.Is(err, ErrSessionIdentity) {
		t.Fatalf("Open(other query) error = %v, want ErrSessionIdentity", err)
	}
	if _, err := service.Open(context.Background(), guardToken, testInitData(t, now, 424242, guardToken, "")); err != nil {
		t.Fatalf("Open(without optional query_id) error = %v", err)
	}

	view, err := service.Open(context.Background(), token, testInitData(t, now, 424242, token, ""))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if view.Status != store.VerificationChallengeIssued || view.Generation != 1 || view.RemainingAttempts != MaxAttempts || view.Prompt == "" {
		t.Fatalf("Open() = %+v", view)
	}
	if _, err := service.Open(context.Background(), token, testInitData(t, now, 424242, "verify_"+token, "")); err != nil {
		t.Fatalf("Open(deep-link start parameter) error = %v", err)
	}
}

func TestFailureRotatesGenerationWithoutExtendingDeadline(t *testing.T) {
	service, database, now := newTestService(t)
	token := createTestSession(t, service, 424242, "join_request", "opaque-request")
	raw := testInitData(t, now, 424242, token, "")
	original, err := database.GetVerificationSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	points := currentTargetPoints(t, database, token)
	wrong := []Point{points[1], points[0]}

	result, err := service.Answer(context.Background(), token, raw, original.Generation, wrong)
	if err != nil {
		t.Fatalf("Answer(wrong) error = %v", err)
	}
	if result.View.Generation != original.Generation+1 || result.View.RemainingAttempts != MaxAttempts-1 || result.View.Status != store.VerificationChallengeIssued {
		t.Fatalf("Answer(wrong) = %+v", result)
	}
	if !result.View.ExpiresAt.Equal(original.ExpiresAt) {
		t.Fatalf("expires_at changed from %v to %v", original.ExpiresAt, result.View.ExpiresAt)
	}
	if _, err := service.Image(context.Background(), token, original.Generation); !errors.Is(err, store.ErrVerificationStale) {
		t.Fatalf("Image(old generation) error = %v, want ErrVerificationStale", err)
	}
	if _, err := service.Answer(context.Background(), token, raw, original.Generation, wrong); !errors.Is(err, store.ErrVerificationStale) {
		t.Fatalf("Answer(old generation) error = %v, want ErrVerificationStale", err)
	}
}

func TestThreeFailuresAwaitReviewAndKeepOriginalExpiry(t *testing.T) {
	service, database, now := newTestService(t)
	token := createTestSession(t, service, 424242, "join_request", "opaque-request")
	raw := testInitData(t, now, 424242, token, "")
	original, err := database.GetVerificationSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		session, err := database.GetVerificationSession(context.Background(), token)
		if err != nil {
			t.Fatal(err)
		}
		points := currentTargetPoints(t, database, token)
		result, err := service.Answer(context.Background(), token, raw, session.Generation, []Point{points[1], points[0]})
		if err != nil {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
		if !result.View.ExpiresAt.Equal(original.ExpiresAt) {
			t.Fatalf("attempt %d extended expires_at", attempt)
		}
		if attempt < MaxAttempts && result.View.Status != store.VerificationChallengeIssued {
			t.Fatalf("attempt %d status = %q", attempt, result.View.Status)
		}
		if attempt == MaxAttempts && result.View.Status != store.VerificationAwaitingReview {
			t.Fatalf("final status = %q, want awaiting_review", result.View.Status)
		}
	}

	stored, err := database.GetVerificationSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Attempts != MaxAttempts || stored.Status != store.VerificationAwaitingReview || len(stored.ChallengeSecret) != 0 {
		t.Fatalf("stored session = %+v", stored)
	}
	if _, err := service.Open(context.Background(), token, raw); !errors.Is(err, store.ErrVerificationReview) {
		t.Fatalf("Open(awaiting review) error = %v", err)
	}
}

func TestSuccessExpiresAndConcurrentDuplicateSubmission(t *testing.T) {
	t.Run("successful answer is consumed once", func(t *testing.T) {
		service, database, now := newTestService(t)
		token := createTestSession(t, service, 424242, "join_request", "opaque-request")
		raw := testInitData(t, now, 424242, token, "")
		points := currentTargetPoints(t, database, token)

		result, err := service.Answer(context.Background(), token, raw, 1, points)
		if err != nil {
			t.Fatalf("Answer(correct) error = %v", err)
		}
		if !result.Correct || result.View.Status != store.VerificationPassed {
			t.Fatalf("Answer(correct) = %+v", result)
		}
		stored, err := database.GetVerificationSession(context.Background(), token)
		if err != nil {
			t.Fatal(err)
		}
		if stored.ConsumedAt == nil || len(stored.ChallengeSecret) != 0 {
			t.Fatalf("completed session retained challenge or lacks consumed_at: %+v", stored)
		}
		if _, err := service.Answer(context.Background(), token, raw, 1, points); !errors.Is(err, store.ErrVerificationCompleted) {
			t.Fatalf("duplicate Answer() error = %v", err)
		}
	})

	t.Run("expired session becomes terminal", func(t *testing.T) {
		service, database, now := newTestService(t)
		token := createTestSession(t, service, 424242, "join_request", "opaque-request")
		raw := testInitData(t, now, 424242, token, "")
		service.now = func() time.Time { return now.Add(DefaultLifetime + time.Second) }
		if _, err := service.Open(context.Background(), token, raw); !errors.Is(err, store.ErrVerificationExpired) {
			t.Fatalf("Open(expired) error = %v", err)
		}
		stored, err := database.GetVerificationSession(context.Background(), token)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != store.VerificationExpired || len(stored.ChallengeSecret) != 0 {
			t.Fatalf("expired session = %+v", stored)
		}
	})

	t.Run("concurrent duplicate has one winner", func(t *testing.T) {
		service, database, now := newTestService(t)
		token := createTestSession(t, service, 424242, "join_request", "opaque-request")
		raw := testInitData(t, now, 424242, token, "")
		points := currentTargetPoints(t, database, token)
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				_, err := service.Answer(context.Background(), token, raw, 1, points)
				results <- err
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		successes, completed := 0, 0
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, store.ErrVerificationCompleted):
				completed++
			default:
				t.Fatalf("unexpected concurrent error: %v", err)
			}
		}
		if successes != 1 || completed != 1 {
			t.Fatalf("successes = %d, completed = %d", successes, completed)
		}
	})
}

func TestVerificationURLRequiresHTTPSPublicBase(t *testing.T) {
	if _, err := VerificationURL("", "token"); !errors.Is(err, ErrPublicURLUnavailable) {
		t.Fatalf("VerificationURL(empty) error = %v", err)
	}
	if _, err := VerificationURL("http://example.com", "token"); !errors.Is(err, ErrPublicURLUnavailable) {
		t.Fatalf("VerificationURL(http) error = %v", err)
	}
	got, err := VerificationURL("https://example.com/tadmin", "abc_123")
	if err != nil {
		t.Fatalf("VerificationURL() error = %v", err)
	}
	if got != "https://example.com/tadmin/verify/abc_123" {
		t.Fatalf("VerificationURL() = %q", got)
	}
}

func newTestService(t *testing.T) (*Service, *store.Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "tadmin.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := database.SaveManagedChat(ctx, testChatID, "Test", "zh-CN", true, now); err != nil {
		t.Fatalf("SaveManagedChat() error = %v", err)
	}
	service := NewService(database, syntheticBotToken)
	service.now = func() time.Time { return now }
	var seed byte = 1
	service.randomBytes = func(target []byte) error {
		for index := range target {
			target[index] = seed + byte(index)
		}
		seed++
		return nil
	}
	return service, database, now
}

func createTestSession(t *testing.T, service *Service, userID int64, sourceKind, sourceRef string) string {
	t.Helper()
	token, err := service.CreateSession(context.Background(), CreateParams{
		ChatID: testChatID, UserID: userID, SourceKind: sourceKind, SourceRef: sourceRef, Language: "zh-CN",
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	return token
}

func testInitData(t *testing.T, now time.Time, userID int64, startParam, queryID string) string {
	t.Helper()
	values := url.Values{
		"auth_date":   {strconv.FormatInt(now.Unix(), 10)},
		"start_param": {startParam},
		"user":        {`{"id":` + strconv.FormatInt(userID, 10) + `}`},
	}
	if queryID != "" {
		values.Set("query_id", queryID)
	}
	return signTestInitData(t, values)
}

func currentTargetPoints(t *testing.T, database *store.Store, token string) []Point {
	t.Helper()
	session, err := database.GetVerificationSession(context.Background(), token)
	if err != nil {
		t.Fatalf("GetVerificationSession() error = %v", err)
	}
	challenge, err := NewChallenge(session.ChallengeSecret, session.Generation)
	if err != nil {
		t.Fatalf("NewChallenge() error = %v", err)
	}
	return targetPoints(challenge)
}
