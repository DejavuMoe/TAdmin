package join

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"tadmin/internal/store"
	"tadmin/internal/verify"
)

type fakeTelegram struct {
	mu       sync.Mutex
	calls    []string
	fail     map[string]error
	admins   map[int64]bool
	message  int
	lastText string
	buttons  []ReviewButton
}

func (f *fakeTelegram) record(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	return f.fail[name]
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

func (f *fakeTelegram) OpenGuardVerification(context.Context, string, string) error {
	return f.record(ActionGuardOpen)
}
func (f *fakeTelegram) AnswerGuardQuery(_ context.Context, _ string, result string) error {
	return f.record("guard_" + result)
}
func (f *fakeTelegram) SendPrivateVerification(_ context.Context, chatID int64, _, _ string) error {
	return f.record("private:" + intString(chatID))
}
func (f *fakeTelegram) DeliverDirectVerification(context.Context, int64, int64, string, string, string) error {
	return f.record(ActionDirectPrompt)
}
func (f *fakeTelegram) ApproveJoinRequest(context.Context, int64, int64) error {
	return f.record(ActionApprove)
}
func (f *fakeTelegram) DeclineJoinRequest(context.Context, int64, int64) error {
	return f.record(ActionDecline)
}
func (f *fakeTelegram) RestrictMember(context.Context, int64, int64) error {
	return f.record(ActionRestrict)
}
func (f *fakeTelegram) RestoreDefaultPermissions(context.Context, int64, int64) error {
	return f.record(ActionRestore)
}
func (f *fakeTelegram) KickMember(context.Context, int64, int64) error {
	return f.record(ActionKick)
}
func (f *fakeTelegram) BanMember(context.Context, int64, int64) error {
	return f.record(ActionBan)
}
func (f *fakeTelegram) IsAdministrator(_ context.Context, _ int64, userID int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.admins[userID], f.fail["administrator"]
}
func (f *fakeTelegram) SendReviewCard(_ context.Context, _ int64, _ int, text string, buttons []ReviewButton) (int, error) {
	if err := f.record("review_send"); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.message++
	f.lastText = text
	f.buttons = append([]ReviewButton(nil), buttons...)
	return 500 + f.message, nil
}
func (f *fakeTelegram) EditReviewCard(_ context.Context, _ int64, _ int, text string) error {
	f.mu.Lock()
	f.lastText = text
	f.mu.Unlock()
	return f.record("review_edit")
}
func (f *fakeTelegram) AnswerCallback(_ context.Context, _ string, _ string, alert bool) error {
	if alert {
		return f.record("callback_alert")
	}
	return f.record("callback_ok")
}

type joinFixture struct {
	ctx      context.Context
	database *store.Store
	service  *verify.Service
	telegram *fakeTelegram
	joins    *Coordinator
	now      time.Time
}

func newJoinFixture(t *testing.T) *joinFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "tadmin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	chat := store.ManagedChat{
		ChatID: -1001, Title: "Source", Enabled: true, Language: "en", ManagementTopicID: 0, ManagementChatID: -2001,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 30 * 24 * 60 * 60,
	}
	if err := database.SaveManagedChatSetup(ctx, chat, now); err != nil {
		t.Fatal(err)
	}
	telegram := &fakeTelegram{fail: map[string]error{}, admins: map[int64]bool{}}
	service := verify.NewService(database, "123456789:test-token")
	coordinator := New(database, service, telegram, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		OwnerUserID: 1, ManagementChatID: -2001,
		PublicBaseURL: "https://verify.example.test", BotUsername: "TAdminTestBot",
	})
	coordinator.now = func() time.Time { return now }
	seed := byte(1)
	coordinator.randomBytes = func(target []byte) error {
		for index := range target {
			target[index] = seed + byte(index)
		}
		seed++
		return nil
	}
	return &joinFixture{ctx: ctx, database: database, service: service, telegram: telegram, joins: coordinator, now: now}
}

func TestGuardOpensBeforeApprovalAndCompletesOnce(t *testing.T) {
	f := newJoinFixture(t)
	request := guardRequest(10, 42, "guard-query-1")
	started := time.Now()
	if err := f.joins.HandleJoinRequest(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Guard initialization took %v", elapsed)
	}
	if f.telegram.count(ActionGuardOpen) != 1 || f.telegram.count("guard_approve") != 0 {
		t.Fatalf("calls = %v", f.telegram.calls)
	}
	item := mustCase(t, f, store.JoinPathGuard, request.ChatID, request.QueryID)
	passSession(t, f, item.VerificationSessionID)
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count("guard_approve") != 1 {
		t.Fatalf("Guard approvals = %d", f.telegram.count("guard_approve"))
	}
	trusted, err := f.database.IsTrusted(f.ctx, request.ChatID, request.Applicant.ID, f.now)
	if err != nil || !trusted {
		t.Fatalf("trusted = %v, err = %v", trusted, err)
	}
	if err := f.joins.HandleJoinRequest(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count(ActionGuardOpen) != 1 || f.telegram.count("guard_approve") != 1 {
		t.Fatalf("duplicate update calls = %v", f.telegram.calls)
	}
}

func TestBlocklistedApplicantIsExplicitlyDeclinedAndBannedBeforeVerification(t *testing.T) {
	f := newJoinFixture(t)
	if _, err := f.database.UpdateListEntry(f.ctx, "block-42", -1001, 42, "block", true, 1, f.now); err != nil {
		t.Fatal(err)
	}
	request := guardRequest(11, 42, "blocked-guard")
	if err := f.joins.HandleJoinRequest(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	if f.telegram.count("guard_decline") != 1 || f.telegram.count(ActionBan) != 1 || f.telegram.count(ActionGuardOpen) != 0 {
		t.Fatalf("calls = %v", f.telegram.calls)
	}
	item := mustCase(t, f, store.JoinPathGuard, request.ChatID, request.QueryID)
	if item.Status != store.JoinResolved || item.Reason != store.RuleBlocklist || item.VerificationSessionID != "" {
		t.Fatalf("blocked case = %+v", item)
	}
	if _, err := f.database.GetModerationActionByKey(f.ctx, "join-log:"+strconv.FormatInt(item.ID, 10)); err != nil {
		t.Fatalf("blocked join log: %v", err)
	}
}

func TestGuardInitializationFailureQueuesAndCreatesOneReview(t *testing.T) {
	f := newJoinFixture(t)
	f.telegram.fail[ActionGuardOpen] = errors.New("unavailable")
	request := guardRequest(11, 43, "guard-query-2")
	if err := f.joins.HandleJoinRequest(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathGuard, request.ChatID, request.QueryID)
	if item.Status != store.JoinReviewPending || f.telegram.count("guard_queue") != 1 {
		t.Fatalf("case = %+v, calls = %v", item, f.telegram.calls)
	}
	f.joins.reconcileAll(f.ctx)
	review, err := f.database.GetReviewByCase(f.ctx, item.ID)
	if err != nil || review.Reason != ReasonUnreachable || f.telegram.count("review_send") != 1 {
		t.Fatalf("review = %+v, err = %v, calls = %v", review, err, f.telegram.calls)
	}
	if err := f.joins.HandleJoinRequest(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count("review_send") != 1 {
		t.Fatalf("duplicate review cards = %d", f.telegram.count("review_send"))
	}
}

func TestGuardWithoutPublicURLQueuesWithoutOpeningOrApproving(t *testing.T) {
	f := newJoinFixture(t)
	f.joins.options.PublicBaseURL = ""
	request := guardRequest(12, 44, "guard-query-no-url")
	if err := f.joins.HandleJoinRequest(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathGuard, request.ChatID, request.QueryID)
	if item.Status != store.JoinReviewPending || f.telegram.count(ActionGuardOpen) != 0 ||
		f.telegram.count("guard_approve") != 0 || f.telegram.count("guard_queue") != 1 {
		t.Fatalf("case = %+v, calls = %v", item, f.telegram.calls)
	}
}

func TestOrdinaryJoinUsesUserChatAndPassesOrReviews(t *testing.T) {
	f := newJoinFixture(t)
	request := ordinaryRequest(20, 50, 900050)
	if err := f.joins.HandleJoinRequest(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	if f.telegram.count("private:900050") != 1 || f.telegram.count(ActionApprove) != 0 {
		t.Fatalf("calls = %v", f.telegram.calls)
	}
	item := mustCase(t, f, store.JoinPathRequest, request.ChatID, "20")
	passSession(t, f, item.VerificationSessionID)
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count(ActionApprove) != 1 {
		t.Fatalf("approve calls = %v", f.telegram.calls)
	}

	f.telegram.fail["private:900051"] = errors.New("private window closed")
	failing := ordinaryRequest(21, 51, 900051)
	if err := f.joins.HandleJoinRequest(f.ctx, failing); err != nil {
		t.Fatal(err)
	}
	failedCase := mustCase(t, f, store.JoinPathRequest, failing.ChatID, "21")
	if failedCase.Status != store.JoinReviewPending {
		t.Fatalf("failed case = %+v", failedCase)
	}
}

func TestDirectJoinRestrictsThenRestoresAndCancelsOnLeave(t *testing.T) {
	f := newJoinFixture(t)
	update := directUpdate(30, 60)
	if err := f.joins.HandleMemberUpdate(f.ctx, update); err != nil {
		t.Fatal(err)
	}
	f.telegram.mu.Lock()
	calls := append([]string(nil), f.telegram.calls...)
	f.telegram.mu.Unlock()
	if len(calls) < 2 || calls[0] != ActionRestrict || calls[1] != ActionDirectPrompt {
		t.Fatalf("direct call order = %v", calls)
	}
	item := mustCase(t, f, store.JoinPathDirect, update.ChatID, "30")
	passSession(t, f, item.VerificationSessionID)
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count(ActionRestore) != 1 {
		t.Fatalf("restore calls = %v", f.telegram.calls)
	}

	second := directUpdate(31, 61)
	if err := f.joins.HandleMemberUpdate(f.ctx, second); err != nil {
		t.Fatal(err)
	}
	leaving := second
	leaving.UpdateID = 32
	leaving.OldStatus = models.ChatMemberTypeRestricted
	leaving.NewStatus = models.ChatMemberTypeLeft
	if err := f.joins.HandleMemberUpdate(f.ctx, leaving); err != nil {
		t.Fatal(err)
	}
	cancelled := mustCase(t, f, store.JoinPathDirect, second.ChatID, "31")
	if cancelled.Status != store.JoinCancelled {
		t.Fatalf("cancelled case = %+v", cancelled)
	}
}

func TestTrustedUserSkipsNewChallenge(t *testing.T) {
	f := newJoinFixture(t)
	first := ordinaryRequest(40, 70, 900070)
	if err := f.joins.HandleJoinRequest(f.ctx, first); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathRequest, first.ChatID, "40")
	passSession(t, f, item.VerificationSessionID)
	f.joins.reconcileAll(f.ctx)
	second := ordinaryRequest(41, 70, 900070)
	if err := f.joins.HandleJoinRequest(f.ctx, second); err != nil {
		t.Fatal(err)
	}
	trustedCase := mustCase(t, f, store.JoinPathRequest, second.ChatID, "41")
	if trustedCase.VerificationSessionID != "" || trustedCase.Status != store.JoinResolved {
		t.Fatalf("trusted case = %+v", trustedCase)
	}
}

func TestTerminalRecoveryScansFailedAndExpiredSessions(t *testing.T) {
	f := newJoinFixture(t)
	failedRequest := ordinaryRequest(50, 80, 900080)
	if err := f.joins.HandleJoinRequest(f.ctx, failedRequest); err != nil {
		t.Fatal(err)
	}
	failed := mustCase(t, f, store.JoinPathRequest, failedRequest.ChatID, "50")
	failSession(t, f, failed.VerificationSessionID)

	expiredRequest := ordinaryRequest(51, 81, 900081)
	if err := f.joins.HandleJoinRequest(f.ctx, expiredRequest); err != nil {
		t.Fatal(err)
	}
	expired := mustCase(t, f, store.JoinPathRequest, expiredRequest.ChatID, "51")
	f.now = f.now.Add(4 * time.Minute)
	f.joins.now = func() time.Time { return f.now }
	f.joins.reconcileAll(f.ctx)

	failedReview, err := f.database.GetReviewByCase(f.ctx, failed.ID)
	if err != nil || failedReview.Reason != ReasonFailed {
		t.Fatalf("failed review = %+v, err = %v", failedReview, err)
	}
	expiredReview, err := f.database.GetReviewByCase(f.ctx, expired.ID)
	if err != nil || expiredReview.Decision != ActionDecline {
		t.Fatalf("expired review = %+v, err = %v", expiredReview, err)
	}
}

func TestReviewAuthorizationConcurrencyAndTrustChoice(t *testing.T) {
	f := newJoinFixture(t)
	f.telegram.fail["private:900090"] = errors.New("blocked")
	request := ordinaryRequest(60, 90, 900090)
	if err := f.joins.HandleJoinRequest(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	f.joins.reconcileAll(f.ctx)
	item := mustCase(t, f, store.JoinPathRequest, request.ChatID, "60")
	review, err := f.database.GetReviewByCase(f.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	callback := ReviewCallback{ID: "cb", ActorUserID: 99, ManagementChat: -2001, MessageThreadID: 77, MessageID: review.ManagementMessageID, Data: "ta:r:" + review.PublicID + ":o"}
	if err := f.joins.HandleReviewCallback(f.ctx, callback); err != nil {
		t.Fatal(err)
	}
	if f.telegram.count(ActionApprove) != 0 || f.telegram.count("callback_alert") == 0 {
		t.Fatalf("unauthorized calls = %v", f.telegram.calls)
	}

	callback.ActorUserID = 1
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			_ = f.joins.HandleReviewCallback(f.ctx, callback)
		}()
	}
	wait.Wait()
	if f.telegram.count(ActionApprove) != 1 {
		t.Fatalf("concurrent approve calls = %d, calls = %v", f.telegram.count(ActionApprove), f.telegram.calls)
	}
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count("review_edit") != 1 {
		t.Fatalf("final review edits = %v", f.telegram.calls)
	}
	trusted, _ := f.database.IsTrusted(f.ctx, request.ChatID, request.Applicant.ID, f.now)
	if trusted {
		t.Fatal("approve once unexpectedly created trust")
	}

	f.telegram.fail["private:900091"] = errors.New("blocked")
	request2 := ordinaryRequest(61, 91, 900091)
	if err := f.joins.HandleJoinRequest(f.ctx, request2); err != nil {
		t.Fatal(err)
	}
	f.joins.reconcileAll(f.ctx)
	item2 := mustCase(t, f, store.JoinPathRequest, request2.ChatID, "61")
	review2, _ := f.database.GetReviewByCase(f.ctx, item2.ID)
	callback = ReviewCallback{ID: "cb2", ActorUserID: 1, ManagementChat: -2001, MessageThreadID: 77, MessageID: review2.ManagementMessageID, Data: "ta:r:" + review2.PublicID + ":t"}
	if err := f.joins.HandleReviewCallback(f.ctx, callback); err != nil {
		t.Fatal(err)
	}
	trusted, _ = f.database.IsTrusted(f.ctx, request2.ChatID, request2.Applicant.ID, f.now)
	if !trusted {
		t.Fatal("approve and trust did not create trust")
	}
}

func TestCurrentAdministratorCanReviewAndAnonymousIdentityCannot(t *testing.T) {
	f := newJoinFixture(t)
	f.telegram.fail["private:900095"] = errors.New("blocked")
	request := ordinaryRequest(65, 95, 900095)
	if err := f.joins.HandleJoinRequest(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	f.joins.reconcileAll(f.ctx)
	item := mustCase(t, f, store.JoinPathRequest, request.ChatID, "65")
	review, _ := f.database.GetReviewByCase(f.ctx, item.ID)
	data := "ta:r:" + review.PublicID + ":d"
	callback := ReviewCallback{ID: "anonymous", ActorUserID: 1, Anonymous: true, ManagementChat: -2001, MessageThreadID: 77, MessageID: review.ManagementMessageID, Data: data}
	if err := f.joins.HandleReviewCallback(f.ctx, callback); err != nil {
		t.Fatal(err)
	}
	if f.telegram.count(ActionDecline) != 0 {
		t.Fatal("anonymous review executed an action")
	}
	f.telegram.admins[99] = true
	callback.ID = "admin"
	callback.ActorUserID = 99
	callback.Anonymous = false
	if err := f.joins.HandleReviewCallback(f.ctx, callback); err != nil {
		t.Fatal(err)
	}
	if f.telegram.count(ActionDecline) != 1 {
		t.Fatalf("administrator decline calls = %v", f.telegram.calls)
	}
}

func TestReviewTimeoutsAndDirectDeclineVersusBan(t *testing.T) {
	f := newJoinFixture(t)
	f.telegram.fail[ActionGuardOpen] = errors.New("offline")
	f.telegram.fail["private:900101"] = errors.New("offline")
	f.telegram.fail[ActionDirectPrompt] = errors.New("offline")
	if err := f.joins.HandleJoinRequest(f.ctx, guardRequest(70, 100, "query-timeout")); err != nil {
		t.Fatal(err)
	}
	if err := f.joins.HandleJoinRequest(f.ctx, ordinaryRequest(71, 101, 900101)); err != nil {
		t.Fatal(err)
	}
	if err := f.joins.HandleMemberUpdate(f.ctx, directUpdate(72, 102)); err != nil {
		t.Fatal(err)
	}
	f.joins.reconcileAll(f.ctx)
	f.now = f.now.Add(31 * time.Minute)
	f.joins.now = func() time.Time { return f.now }
	f.joins.reconcileAll(f.ctx)
	if f.telegram.count(ActionDecline) != 2 || f.telegram.count(ActionKick) != 1 {
		t.Fatalf("timeout actions = %v", f.telegram.calls)
	}

	// A direct manual ban uses BanMember and never the kick pair.
	f.telegram.fail[ActionDirectPrompt] = errors.New("offline again")
	update := directUpdate(73, 103)
	if err := f.joins.HandleMemberUpdate(f.ctx, update); err != nil {
		t.Fatal(err)
	}
	f.joins.reconcileAll(f.ctx)
	item := mustCase(t, f, store.JoinPathDirect, update.ChatID, "73")
	review, _ := f.database.GetReviewByCase(f.ctx, item.ID)
	callback := ReviewCallback{ID: "ban", ActorUserID: 1, ManagementChat: -2001, MessageThreadID: 77, MessageID: review.ManagementMessageID, Data: "ta:r:" + review.PublicID + ":b"}
	if err := f.joins.HandleReviewCallback(f.ctx, callback); err != nil {
		t.Fatal(err)
	}
	if f.telegram.count(ActionBan) != 1 {
		t.Fatalf("ban calls = %v", f.telegram.calls)
	}
}

func TestPrivateStartRejectsInvalidExpiredAndCrossUser(t *testing.T) {
	f := newJoinFixture(t)
	request := ordinaryRequest(80, 110, 900110)
	if err := f.joins.HandleJoinRequest(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	item := mustCase(t, f, store.JoinPathRequest, request.ChatID, "80")
	if err := f.joins.HandlePrivateStart(f.ctx, 110, 110, item.VerificationSessionID); err != nil {
		t.Fatalf("valid start error = %v", err)
	}
	if f.telegram.count("private:110") != 1 {
		t.Fatalf("valid start calls = %v", f.telegram.calls)
	}
	if err := f.joins.HandlePrivateStart(f.ctx, 110, 111, item.VerificationSessionID); !errors.Is(err, ErrStartIdentity) {
		t.Fatalf("cross-user error = %v", err)
	}
	if err := f.joins.HandlePrivateStart(f.ctx, 110, 110, "invalid"); !errors.Is(err, ErrStartInvalid) {
		t.Fatalf("invalid error = %v", err)
	}
	f.now = f.now.Add(4 * time.Minute)
	f.joins.now = func() time.Time { return f.now }
	if err := f.joins.HandlePrivateStart(f.ctx, 110, 110, item.VerificationSessionID); !errors.Is(err, ErrStartInvalid) {
		t.Fatalf("expired error = %v", err)
	}
}

func TestReviewTextAndPersistedMetadataDoNotContainSecrets(t *testing.T) {
	f := newJoinFixture(t)
	f.telegram.fail[ActionGuardOpen] = errors.New("offline")
	queryID := "SECRET-GUARD-QUERY"
	if err := f.joins.HandleJoinRequest(f.ctx, guardRequest(90, 120, queryID)); err != nil {
		t.Fatal(err)
	}
	f.joins.reconcileAll(f.ctx)
	f.telegram.mu.Lock()
	text := f.telegram.lastText
	f.telegram.mu.Unlock()
	for _, secret := range []string{"123456789:test-token", queryID, "challenge_secret", "init_data", "https://t.me/+"} {
		if bytes.Contains([]byte(text), []byte(secret)) {
			t.Fatalf("review text contains %q: %s", secret, text)
		}
	}
}

func guardRequest(updateID, userID int64, queryID string) JoinRequest {
	return JoinRequest{UpdateID: updateID, ChatID: -1001, ChatTitle: "Source", QueryID: queryID,
		Applicant: Applicant{ID: userID, FirstName: "Test", Username: "tester"}, InviteSource: "named invite"}
}

func ordinaryRequest(updateID, userID, userChatID int64) JoinRequest {
	return JoinRequest{UpdateID: updateID, ChatID: -1001, ChatTitle: "Source", UserChatID: userChatID,
		Applicant: Applicant{ID: userID, FirstName: "Test", Username: "tester"}, InviteSource: "named invite"}
}

func directUpdate(updateID, userID int64) MemberUpdate {
	return MemberUpdate{UpdateID: updateID, ChatID: -1001, ChatTitle: "Source",
		Applicant: Applicant{ID: userID, FirstName: "Test", Username: "tester"},
		OldStatus: models.ChatMemberTypeLeft, NewStatus: models.ChatMemberTypeMember,
		JoinedAtUnix: time.Now().Unix(), InviteSource: "named invite"}
}

func mustCase(t *testing.T, f *joinFixture, path string, chatID int64, sourceRef string) store.JoinCase {
	t.Helper()
	item, err := f.database.GetJoinCaseBySourceKey(f.ctx, sourceKey(path, chatID, sourceRef))
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func passSession(t *testing.T, f *joinFixture, token string) {
	t.Helper()
	session, err := f.database.GetVerificationSession(f.ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SubmitVerification(f.ctx, token, session.Generation, true, nil, verify.MaxAttempts, f.now); err != nil {
		t.Fatal(err)
	}
}

func failSession(t *testing.T, f *joinFixture, token string) {
	t.Helper()
	for attempt := 0; attempt < verify.MaxAttempts; attempt++ {
		session, err := f.database.GetVerificationSession(f.ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		var replacement []byte
		if attempt+1 < verify.MaxAttempts {
			replacement = bytes.Repeat([]byte{byte(attempt + 1)}, 32)
		}
		if _, err := f.database.SubmitVerification(f.ctx, token, session.Generation, false, replacement, verify.MaxAttempts, f.now); err != nil {
			t.Fatal(err)
		}
	}
}

func intString(value int64) string {
	return strconv.FormatInt(value, 10)
}

func (f *fakeTelegram) MemberStatus(_ context.Context, _ int64, userID int64) (models.ChatMemberType, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.admins[userID] {
		return models.ChatMemberTypeAdministrator, f.fail["administrator"]
	}
	return models.ChatMemberTypeMember, f.fail["administrator"]
}

func (c *Coordinator) reconcileAll(ctx context.Context) {
	c.reconcile(ctx)
	if err := c.reconcileReviewCards(ctx); err != nil {
		c.logError("test reconcile cards", 0, err)
	}
}

func (f *fakeTelegram) RestorePriorPermissions(context.Context, int64, int64, string, int64) error {
	return f.record("restore_prior")
}
