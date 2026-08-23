package join

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"

	"tadmin/internal/store"
	"tadmin/internal/verify"
)

const (
	defaultReviewLifetime = 30 * time.Minute
	recentApprovalWindow  = 10 * time.Minute
	actionRetryAge        = 15 * time.Second
	coordinatorInterval   = 2 * time.Second
)

type Options struct {
	OwnerUserID      int64
	ManagementChatID int64
	PublicBaseURL    string
	BotUsername      string
}

type Coordinator struct {
	database     *store.Store
	verification *verify.Service
	telegram     Telegram
	logger       *slog.Logger
	options      Options
	now          func() time.Time
	randomBytes  func([]byte) error
	wake         chan struct{}
}

func New(database *store.Store, verification *verify.Service, telegram Telegram, logger *slog.Logger, options Options) *Coordinator {
	return &Coordinator{
		database:     database,
		verification: verification,
		telegram:     telegram,
		logger:       logger,
		options:      options,
		now:          time.Now,
		randomBytes:  randomFill,
		wake:         make(chan struct{}, 1),
	}
}

func (c *Coordinator) Wake() {
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Coordinator) Run(ctx context.Context) {
	if c == nil || c.database == nil || c.verification == nil || c.telegram == nil {
		return
	}
	ticker := time.NewTicker(coordinatorInterval)
	defer ticker.Stop()
	c.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.reconcile(ctx)
		case <-c.wake:
			c.reconcile(ctx)
		}
	}
}

func (c *Coordinator) HandleJoinRequest(ctx context.Context, request JoinRequest) error {
	chat, err := c.database.GetManagedChat(ctx, request.ChatID)
	if errors.Is(err, store.ErrManagedChatNotFound) || (err == nil && !chat.Enabled) {
		return nil
	}
	if err != nil {
		return err
	}
	if request.Applicant.ID <= 0 || request.Applicant.IsBot {
		return nil
	}
	blocked, err := c.database.IsBlocked(ctx, request.ChatID, request.Applicant.ID)
	if err != nil {
		return err
	}
	path := store.JoinPathRequest
	sourceRef := strconv.FormatInt(request.UpdateID, 10)
	if request.QueryID != "" {
		path = store.JoinPathGuard
		sourceRef = request.QueryID
	}
	item := store.JoinCase{
		SourceKey:     sourceKey(path, request.ChatID, sourceRef),
		UpdateID:      request.UpdateID,
		Path:          path,
		ChatID:        request.ChatID,
		UserID:        request.Applicant.ID,
		UserChatID:    request.UserChatID,
		SourceRef:     sourceRef,
		InviteSource:  cleanInviteSource(request.InviteSource),
		UserFirstName: cleanLabel(request.Applicant.FirstName, 64),
		UserLastName:  cleanLabel(request.Applicant.LastName, 64),
		Username:      cleanUsername(request.Applicant.Username),
	}
	if blocked {
		item.Reason = store.RuleBlocklist
	}
	stored, created, err := c.database.CreateJoinCase(ctx, item, c.currentTime())
	if err != nil || !created {
		if !created {
			c.Wake()
		}
		return err
	}
	if blocked {
		if err := c.database.ScheduleCaseAction(ctx, stored.ID, ActionBan, c.currentTime()); err != nil {
			return err
		}
		return c.processAction(ctx, stored.ID)
	}
	return c.initializeCase(ctx, stored, chat)
}

func (c *Coordinator) HandleMemberUpdate(ctx context.Context, update MemberUpdate) error {
	if leavingMembership(update.OldStatus, update.NewStatus) {
		return c.database.CancelDirectJoin(ctx, update.ChatID, update.Applicant.ID, c.currentTime())
	}
	if !enteringMembership(update.OldStatus, update.NewStatus) || update.Applicant.IsBot || update.Applicant.ID <= 0 {
		return nil
	}
	chat, err := c.database.GetManagedChat(ctx, update.ChatID)
	if errors.Is(err, store.ErrManagedChatNotFound) || (err == nil && !chat.Enabled) {
		return nil
	}
	if err != nil {
		return err
	}
	checkContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	administrator, err := c.telegram.IsAdministrator(checkContext, update.ChatID, update.Applicant.ID)
	cancel()
	if err != nil {
		return err
	}
	if administrator {
		return nil
	}
	blocked, err := c.database.IsBlocked(ctx, update.ChatID, update.Applicant.ID)
	if err != nil {
		return err
	}
	now := c.currentTime()
	joinedAt := time.Unix(update.JoinedAtUnix, 0).UTC()
	if update.JoinedAtUnix <= 0 || joinedAt.After(now.Add(time.Minute)) {
		joinedAt = now
	}
	sourceRef := strconv.FormatInt(update.UpdateID, 10)
	item := store.JoinCase{
		SourceKey:     sourceKey(store.JoinPathDirect, update.ChatID, sourceRef),
		UpdateID:      update.UpdateID,
		Path:          store.JoinPathDirect,
		ChatID:        update.ChatID,
		UserID:        update.Applicant.ID,
		SourceRef:     sourceRef,
		InviteSource:  cleanInviteSource(update.InviteSource),
		UserFirstName: cleanLabel(update.Applicant.FirstName, 64),
		UserLastName:  cleanLabel(update.Applicant.LastName, 64),
		Username:      cleanUsername(update.Applicant.Username),
		JoinedAt:      &joinedAt,
	}
	if blocked {
		item.Reason = store.RuleBlocklist
	}
	stored, created, err := c.database.CreateJoinCase(ctx, item, now)
	if err != nil || !created {
		if !created {
			c.Wake()
		}
		return err
	}
	if blocked {
		if err := c.database.ScheduleCaseAction(ctx, stored.ID, ActionBan, c.currentTime()); err != nil {
			return err
		}
		return c.processAction(ctx, stored.ID)
	}
	return c.initializeCase(ctx, stored, chat)
}

func (c *Coordinator) HandlePrivateStart(ctx context.Context, privateChatID, userID int64, token string) error {
	if !validToken(token) || privateChatID == 0 || userID <= 0 {
		return ErrStartInvalid
	}
	session, err := c.database.GetActiveVerificationSession(ctx, token, c.currentTime())
	if err != nil {
		return ErrStartInvalid
	}
	if session.UserID != userID {
		return ErrStartIdentity
	}
	url, err := verify.VerificationURL(c.options.PublicBaseURL, token)
	if err != nil {
		return ErrStartInvalid
	}
	sendContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return c.telegram.SendPrivateVerification(sendContext, privateChatID, verificationPrompt(session.Language), url)
}

func (c *Coordinator) HandleReviewCallback(ctx context.Context, callback ReviewCallback) error {
	publicID, decision, ok := parseCallbackData(callback.Data)
	if !ok {
		return nil
	}
	deny := func(text string) error {
		return c.telegram.AnswerCallback(ctx, callback.ID, text, true)
	}
	review, err := c.database.GetReviewByPublicID(ctx, publicID)
	if errors.Is(err, store.ErrReviewNotFound) {
		return deny("This review is no longer available.")
	}
	if err != nil {
		return err
	}
	item, err := c.database.GetJoinCase(ctx, review.JoinCaseID)
	if err != nil {
		return err
	}
	chat, err := c.database.GetManagedChat(ctx, item.ChatID)
	if err != nil {
		return err
	}
	if callback.Anonymous || callback.ActorUserID <= 0 {
		return deny(callbackMessage(chat.Language, "identity"))
	}
	if callback.ManagementChat != c.options.ManagementChatID || callback.MessageThreadID != review.ManagementTopicID ||
		(review.ManagementMessageID != 0 && callback.MessageID != review.ManagementMessageID) {
		return deny(callbackMessage(chat.Language, "location"))
	}
	authorized := callback.ActorUserID == c.options.OwnerUserID
	if !authorized {
		checkContext, cancel := context.WithTimeout(ctx, 8*time.Second)
		authorized, err = c.telegram.IsAdministrator(checkContext, item.ChatID, callback.ActorUserID)
		cancel()
		if err != nil {
			return err
		}
	}
	if !authorized {
		return deny(callbackMessage(chat.Language, "unauthorized"))
	}
	claim, err := c.database.ClaimReviewDecision(ctx, publicID, decision, callback.ActorUserID, c.currentTime())
	if errors.Is(err, store.ErrReviewProcessed) {
		return deny(callbackMessage(chat.Language, "processed"))
	}
	if errors.Is(err, store.ErrActionUnavailable) {
		c.Wake()
		return deny(callbackMessage(chat.Language, "preparing"))
	}
	if err != nil {
		return err
	}
	if err := c.telegram.AnswerCallback(ctx, callback.ID, callbackMessage(chat.Language, "processing"), false); err != nil {
		c.logError("answer review callback", claim.Case.ID, err)
	}
	if err := c.processAction(ctx, claim.Case.ID); err != nil && !errors.Is(err, store.ErrActionUnavailable) {
		c.Wake()
		return err
	}
	c.Wake()
	return nil
}

func (c *Coordinator) initializeCase(ctx context.Context, initial store.JoinCase, chat store.ManagedChat) error {
	item, err := c.database.GetJoinCase(ctx, initial.ID)
	if err != nil || item.Status != store.JoinInitializing {
		return err
	}
	now := c.currentTime()
	if item.Reason == store.RuleBlocklist {
		if item.ActionKind != ActionBan || item.ActionState == store.ActionNone {
			if err := c.database.ScheduleCaseAction(ctx, item.ID, ActionBan, now); err != nil {
				return err
			}
		}
		return c.processAction(ctx, item.ID)
	}
	trusted, err := c.database.IsTrusted(ctx, item.ChatID, item.UserID, now)
	if err != nil {
		return err
	}
	if trusted {
		action := ActionApprove
		if item.Path == store.JoinPathGuard {
			action = ActionGuardApprove
		} else if item.Path == store.JoinPathDirect {
			action = ActionRecordMember
		}
		if err := c.database.ScheduleCaseAction(ctx, item.ID, action, now); err != nil {
			return err
		}
		if err := c.processAction(ctx, item.ID); errors.Is(err, store.ErrActionUnavailable) {
			return nil
		} else {
			return err
		}
	}
	if item.Path == store.JoinPathDirect {
		handled, err := c.database.HasResolvedApplication(ctx, item.ChatID, item.UserID, now.Add(-recentApprovalWindow))
		if err != nil {
			return err
		}
		if handled {
			if err := c.database.ScheduleCaseAction(ctx, item.ID, ActionRecordMember, now); err != nil {
				return err
			}
			if err := c.processAction(ctx, item.ID); errors.Is(err, store.ErrActionUnavailable) {
				return nil
			} else {
				return err
			}
		}
		if item.ActionKind == "" || (item.ActionKind == ActionRestrict && item.ActionState != store.ActionCompleted) {
			if item.ActionKind == "" {
				if err := c.database.ScheduleCaseAction(ctx, item.ID, ActionRestrict, now); err != nil {
					return err
				}
			}
			if err := c.processAction(ctx, item.ID); errors.Is(err, store.ErrActionUnavailable) {
				return nil
			} else if err != nil {
				return err
			}
			item, err = c.database.GetJoinCase(ctx, item.ID)
			if err != nil {
				return err
			}
		}
	}
	if item.VerificationSessionID == "" {
		sourceKind := "join_request"
		if item.Path == store.JoinPathGuard {
			sourceKind = verify.SourceGuardQuery
		} else if item.Path == store.JoinPathDirect {
			sourceKind = "direct_join"
		}
		token, _, err := c.verification.CreateOrGetSession(ctx, verify.CreateParams{
			ChatID: item.ChatID, UserID: item.UserID, SourceKind: sourceKind,
			SourceRef: item.SourceRef, Language: chat.Language,
			Lifetime: time.Duration(chat.VerificationTimeoutSeconds) * time.Second,
		})
		if err != nil {
			return c.failInitialization(ctx, item, err)
		}
		if err := c.database.AttachVerificationSession(ctx, item.ID, token, now); err != nil {
			return err
		}
		item.VerificationSessionID = token
	}
	if _, err := verify.VerificationURL(c.options.PublicBaseURL, item.VerificationSessionID); err != nil {
		return c.failInitialization(ctx, item, err)
	}
	action := ActionPrivatePrompt
	if item.Path == store.JoinPathGuard {
		action = ActionGuardOpen
	} else if item.Path == store.JoinPathDirect {
		action = ActionDirectPrompt
	}
	if item.ActionKind != action || item.ActionState != store.ActionCompleted {
		if err := c.database.ScheduleCaseAction(ctx, item.ID, action, now); err != nil {
			return err
		}
		if err := c.processAction(ctx, item.ID); errors.Is(err, store.ErrActionUnavailable) {
			return nil
		} else if err != nil {
			return c.failInitialization(ctx, item, err)
		}
	}
	if err := c.database.MarkCaseVerifying(ctx, item.ID, now); err != nil {
		return err
	}
	c.Wake()
	return nil
}

func (c *Coordinator) failInitialization(ctx context.Context, item store.JoinCase, cause error) error {
	queue := item.Path == store.JoinPathGuard
	if err := c.database.EnterReview(ctx, item.ID, ReasonUnreachable, queue, c.currentTime()); err != nil {
		return errors.Join(cause, err)
	}
	if err := c.ensureReview(ctx, item.ID); err != nil {
		return errors.Join(cause, err)
	}
	if queue {
		if err := c.processAction(ctx, item.ID); err != nil && !errors.Is(err, store.ErrActionUnavailable) {
			c.logError("queue unavailable Guard request", item.ID, err)
		}
	}
	c.Wake()
	return nil
}

func (c *Coordinator) reconcile(ctx context.Context) {
	now := c.currentTime()
	if _, err := c.database.ExpireDueVerificationSessions(ctx, now); err != nil {
		c.logError("expire verification sessions", 0, err)
		return
	}
	claims, err := c.database.ClaimExpiredReviews(ctx, now, 50)
	if err != nil {
		c.logError("claim expired reviews", 0, err)
	} else {
		for _, claim := range claims {
			if err := c.processAction(ctx, claim.Case.ID); err != nil && !errors.Is(err, store.ErrActionUnavailable) {
				c.logError("execute review timeout", claim.Case.ID, err)
			}
		}
	}
	cases, err := c.database.ListOpenJoinCases(ctx, 200)
	if err != nil {
		c.logError("list join cases", 0, err)
		return
	}
	for _, item := range cases {
		select {
		case <-ctx.Done():
			return
		default:
		}
		chat, err := c.database.GetManagedChat(ctx, item.ChatID)
		if err != nil || !chat.Enabled {
			continue
		}
		switch item.Status {
		case store.JoinInitializing:
			err = c.initializeCase(ctx, item, chat)
		case store.JoinVerifying:
			err = c.reconcileVerification(ctx, item, chat)
		case store.JoinReviewPending:
			err = c.ensureReview(ctx, item.ID)
			if err == nil && (item.ActionState == store.ActionPending || item.ActionState == store.ActionFailed ||
				(item.ActionState == store.ActionProcessing && !item.UpdatedAt.After(now.Add(-actionRetryAge)))) {
				err = c.processAction(ctx, item.ID)
			}
		}
		if err != nil && !errors.Is(err, store.ErrActionUnavailable) {
			c.logError("reconcile join case", item.ID, err)
		}
	}
	if err := c.reconcileReviewCards(ctx); err != nil {
		c.logError("reconcile review cards", 0, err)
	}
}

func (c *Coordinator) reconcileVerification(ctx context.Context, item store.JoinCase, chat store.ManagedChat) error {
	if item.VerificationSessionID == "" {
		return c.database.EnterReview(ctx, item.ID, ReasonUnreachable, item.Path == store.JoinPathGuard, c.currentTime())
	}
	session, err := c.database.GetVerificationSession(ctx, item.VerificationSessionID)
	if err != nil {
		return err
	}
	switch session.Status {
	case store.VerificationChallengeIssued, store.VerificationPending:
		return nil
	case store.VerificationPassed:
		action := ActionApprove
		if item.Path == store.JoinPathGuard {
			action = ActionGuardApprove
		} else if item.Path == store.JoinPathDirect {
			action = ActionRestore
		}
		if err := c.database.ScheduleCaseAction(ctx, item.ID, action, c.currentTime()); err != nil {
			return err
		}
		return c.processAction(ctx, item.ID)
	case store.VerificationAwaitingReview:
		if err := c.database.EnterReview(ctx, item.ID, ReasonFailed, item.Path == store.JoinPathGuard, c.currentTime()); err != nil {
			return err
		}
	case store.VerificationExpired:
		if err := c.database.EnterReview(ctx, item.ID, ReasonExpired, item.Path == store.JoinPathGuard, c.currentTime()); err != nil {
			return err
		}
	case store.VerificationCancelled:
		if err := c.database.EnterReview(ctx, item.ID, ReasonUnreachable, item.Path == store.JoinPathGuard, c.currentTime()); err != nil {
			return err
		}
	default:
		return store.ErrJoinCaseState
	}
	if err := c.ensureReview(ctx, item.ID); err != nil {
		return err
	}
	if item.Path == store.JoinPathGuard {
		return c.processAction(ctx, item.ID)
	}
	_ = chat
	return nil
}

func (c *Coordinator) ensureReview(ctx context.Context, caseID int64) error {
	if _, err := c.database.GetReviewByCase(ctx, caseID); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrReviewNotFound) {
		return err
	}
	publicID, err := c.newPublicID()
	if err != nil {
		return err
	}
	_, _, err = c.database.EnsureReview(ctx, caseID, publicID, c.currentTime().Add(defaultReviewLifetime), c.currentTime())
	return err
}

func (c *Coordinator) processAction(ctx context.Context, caseID int64) error {
	now := c.currentTime()
	item, err := c.database.ClaimCaseAction(ctx, caseID, now.Add(-actionRetryAge), now)
	if err != nil {
		return err
	}
	chat, err := c.database.GetManagedChat(ctx, item.ChatID)
	if err != nil {
		_ = c.database.FailCaseAction(ctx, item.ID, item.ActionKind, now)
		return err
	}
	actionContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	err = c.executeTelegramAction(actionContext, item, chat)
	cancel()
	if err != nil {
		_ = c.database.FailCaseAction(ctx, item.ID, item.ActionKind, now)
		return err
	}
	nextStatus := store.JoinResolved
	trustSeconds := 0
	initializeMember := false
	switch item.ActionKind {
	case ActionGuardOpen, ActionPrivatePrompt, ActionDirectPrompt, ActionRestrict:
		nextStatus = store.JoinInitializing
	case ActionGuardQueue:
		nextStatus = store.JoinReviewPending
	case ActionGuardApprove, ActionApprove, ActionRestore:
		initializeMember = true
		review, reviewErr := c.database.GetReviewByCase(ctx, item.ID)
		if errors.Is(reviewErr, store.ErrReviewNotFound) {
			trustSeconds = chat.ReentryTrustSeconds
		} else if reviewErr == nil && review.Decision == store.ReviewDecisionTrust {
			trustSeconds = chat.ReentryTrustSeconds
		}
	case ActionRecordMember:
		initializeMember = true
	}
	if err := c.database.CompleteCaseAction(ctx, item.ID, item.ActionKind, nextStatus, trustSeconds, initializeMember, now); err != nil {
		return err
	}
	if nextStatus == store.JoinInitializing {
		c.Wake()
	}
	return nil
}

func (c *Coordinator) executeTelegramAction(ctx context.Context, item store.JoinCase, chat store.ManagedChat) error {
	var verificationURL string
	if item.VerificationSessionID != "" {
		verificationURL, _ = verify.VerificationURL(c.options.PublicBaseURL, item.VerificationSessionID)
	}
	switch item.ActionKind {
	case ActionGuardOpen:
		if verificationURL == "" {
			return verify.ErrPublicURLUnavailable
		}
		return c.telegram.OpenGuardVerification(ctx, item.SourceRef, verificationURL)
	case ActionGuardApprove:
		return c.telegram.AnswerGuardQuery(ctx, item.SourceRef, "approve")
	case ActionGuardQueue:
		return c.telegram.AnswerGuardQuery(ctx, item.SourceRef, "queue")
	case ActionPrivatePrompt:
		if verificationURL == "" || item.UserChatID == 0 {
			return ErrStartInvalid
		}
		return c.telegram.SendPrivateVerification(ctx, item.UserChatID, verificationPrompt(chat.Language), verificationURL)
	case ActionDirectPrompt:
		if verificationURL == "" {
			return ErrStartInvalid
		}
		deepLink := "https://t.me/" + c.options.BotUsername + "?start=verify_" + item.VerificationSessionID
		return c.telegram.DeliverDirectVerification(ctx, item.ChatID, item.UserID, verificationPrompt(chat.Language), verificationURL, deepLink)
	case ActionApprove:
		return c.telegram.ApproveJoinRequest(ctx, item.ChatID, item.UserID)
	case ActionDecline:
		return c.telegram.DeclineJoinRequest(ctx, item.ChatID, item.UserID)
	case ActionRestrict:
		return c.telegram.RestrictMember(ctx, item.ChatID, item.UserID)
	case ActionRestore:
		return c.telegram.RestoreDefaultPermissions(ctx, item.ChatID, item.UserID)
	case ActionKick:
		return c.telegram.KickMember(ctx, item.ChatID, item.UserID)
	case ActionBan:
		var declineErr error
		if item.Path == store.JoinPathGuard {
			declineErr = c.telegram.AnswerGuardQuery(ctx, item.SourceRef, "decline")
		} else if item.Path != store.JoinPathDirect {
			declineErr = c.telegram.DeclineJoinRequest(ctx, item.ChatID, item.UserID)
		}
		return errors.Join(declineErr, c.telegram.BanMember(ctx, item.ChatID, item.UserID))
	case ActionRecordMember:
		return nil
	default:
		return store.ErrJoinCaseState
	}
}

func (c *Coordinator) reconcileReviewCards(ctx context.Context) error {
	now := c.currentTime()
	claims, err := c.database.ListClaimableReviewCards(ctx, now.Add(-actionRetryAge), 50)
	if err != nil {
		return err
	}
	for _, claim := range claims {
		review, err := c.database.ClaimReviewCard(ctx, claim.Review.ID, now.Add(-actionRetryAge), now)
		if errors.Is(err, store.ErrActionUnavailable) {
			continue
		}
		if err != nil {
			return err
		}
		claim.Review = review
		text, buttons, err := c.reviewCard(ctx, claim)
		if err != nil {
			_ = c.database.FailReviewCard(ctx, review.ID, now)
			continue
		}
		messageID := review.ManagementMessageID
		cardContext, cancel := context.WithTimeout(ctx, 8*time.Second)
		if review.Status == store.ReviewPending && messageID == 0 {
			messageID, err = c.telegram.SendReviewCard(cardContext, c.options.ManagementChatID, review.ManagementTopicID, text, buttons)
		} else if review.Status == store.ReviewCompleted {
			if messageID == 0 {
				messageID, err = c.telegram.SendReviewCard(cardContext, c.options.ManagementChatID, review.ManagementTopicID, text, nil)
			} else {
				err = c.telegram.EditReviewCard(cardContext, c.options.ManagementChatID, messageID, text)
			}
		} else {
			err = store.ErrJoinCaseState
		}
		cancel()
		if err != nil {
			_ = c.database.FailReviewCard(ctx, review.ID, now)
			continue
		}
		if err := c.database.CompleteReviewCard(ctx, review.ID, messageID, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) reviewCard(ctx context.Context, claim store.ReviewClaim) (string, []ReviewButton, error) {
	chat, err := c.database.GetManagedChat(ctx, claim.Case.ChatID)
	if err != nil {
		return "", nil, err
	}
	var session *store.VerificationSession
	if claim.Case.VerificationSessionID != "" {
		value, sessionErr := c.database.GetVerificationSession(ctx, claim.Case.VerificationSessionID)
		if sessionErr == nil {
			session = &value
		}
	}
	text := formatReview(chat, claim, session)
	if claim.Review.Status == store.ReviewCompleted {
		return text, nil, nil
	}
	labels := reviewLabels(chat.Language)
	prefix := "ta:r:" + claim.Review.PublicID + ":"
	return text, []ReviewButton{
		{Text: labels[0], Data: prefix + "o"},
		{Text: labels[1], Data: prefix + "t"},
		{Text: labels[2], Data: prefix + "d"},
		{Text: labels[3], Data: prefix + "b"},
	}, nil
}

func (c *Coordinator) currentTime() time.Time {
	return c.now().UTC().Truncate(time.Second)
}

func (c *Coordinator) newPublicID() (string, error) {
	value := make([]byte, 9)
	if err := c.randomBytes(value); err != nil {
		return "", fmt.Errorf("create review identifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (c *Coordinator) logError(operation string, caseID int64, err error) {
	if c.logger == nil || err == nil {
		return
	}
	if caseID == 0 {
		c.logger.Error(operation, "error", err)
		return
	}
	c.logger.Error(operation, "join_case_id", caseID, "error", err)
}

func sourceKey(path string, chatID int64, sourceRef string) string {
	digest := sha256.Sum256([]byte(path + "\x00" + strconv.FormatInt(chatID, 10) + "\x00" + sourceRef))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func cleanLabel(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, "\x00", "")
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func cleanUsername(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "@")
	if len(value) > 32 {
		value = value[:32]
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_') {
			return ""
		}
	}
	return value
}

func cleanInviteSource(value string) string {
	value = cleanLabel(value, 96)
	lower := strings.ToLower(value)
	if strings.Contains(lower, "://") || strings.Contains(lower, "t.me/") || strings.Contains(lower, "joinchat") {
		return "named"
	}
	return value
}

func enteringMembership(oldStatus, newStatus models.ChatMemberType) bool {
	oldOutside := oldStatus == models.ChatMemberTypeLeft || oldStatus == models.ChatMemberTypeBanned
	newInside := newStatus == models.ChatMemberTypeMember || newStatus == models.ChatMemberTypeRestricted
	return oldOutside && newInside
}

func leavingMembership(oldStatus, newStatus models.ChatMemberType) bool {
	oldInside := oldStatus == models.ChatMemberTypeMember || oldStatus == models.ChatMemberTypeRestricted
	newOutside := newStatus == models.ChatMemberTypeLeft || newStatus == models.ChatMemberTypeBanned
	return oldInside && newOutside
}

func parseCallbackData(value string) (string, string, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 4 || parts[0] != "ta" || parts[1] != "r" || len(parts[2]) < 8 || len(parts[2]) > 24 {
		return "", "", false
	}
	decision := map[string]string{
		"o": store.ReviewDecisionOnce,
		"t": store.ReviewDecisionTrust,
		"d": store.ReviewDecisionDeny,
		"b": store.ReviewDecisionBan,
	}[parts[3]]
	return parts[2], decision, decision != ""
}

func validToken(token string) bool {
	if len(token) < 22 || len(token) > 128 {
		return false
	}
	for _, character := range token {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_') {
			return false
		}
	}
	return true
}

func randomFill(target []byte) error {
	_, err := rand.Read(target)
	return err
}
