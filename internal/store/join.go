package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	JoinPathGuard       = "guard"
	JoinPathRequest     = "join_request"
	JoinPathDirect      = "direct"
	JoinInitializing    = "initializing"
	JoinVerifying       = "verifying"
	JoinReviewPending   = "review_pending"
	JoinResolved        = "resolved"
	JoinCancelled       = "cancelled"
	ActionNone          = "none"
	ActionPending       = "pending"
	ActionProcessing    = "processing"
	ActionCompleted     = "completed"
	ActionFailed        = "failed"
	ReviewPending       = "pending"
	ReviewProcessing    = "processing"
	ReviewCompleted     = "completed"
	ReviewDecisionOnce  = "approve_once"
	ReviewDecisionTrust = "approve_trust"
	ReviewDecisionDeny  = "decline"
	ReviewDecisionBan   = "ban"
	ReviewDecisionTime  = "timeout"
)

var (
	ErrManagedChatNotFound = errors.New("managed chat not found")
	ErrJoinCaseNotFound    = errors.New("join case not found")
	ErrJoinCaseState       = errors.New("invalid join case state")
	ErrReviewNotFound      = errors.New("review item not found")
	ErrReviewProcessed     = errors.New("review item already processed")
	ErrActionUnavailable   = errors.New("join action is not available")
)

type ManagedChat struct {
	ChatID                     int64
	Title                      string
	Enabled                    bool
	ModerationEnabled          bool
	Language                   string
	ManagementTopicID          int
	ManagementChatID           int64
	VerificationTimeoutSeconds int
	ReentryTrustSeconds        int
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
}

type JoinCase struct {
	ID                    int64
	DeadlineAt            time.Time
	PriorPermissions      string
	PriorUntil            int64
	SourceKey             string
	UpdateID              int64
	Path                  string
	ChatID                int64
	UserID                int64
	UserChatID            int64
	SourceRef             string
	InviteSource          string
	UserFirstName         string
	UserLastName          string
	Username              string
	VerificationSessionID string
	Status                string
	Reason                string
	ActionKind            string
	ActionState           string
	ActionAttempts        int
	JoinedAt              *time.Time
	ResolvedAt            *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type ReviewItem struct {
	ID                  int64
	PublicID            string
	JoinCaseID          int64
	ChatID              int64
	UserID              int64
	VerificationSession string
	Status              string
	Reason              string
	DeadlineAt          time.Time
	ManagementTopicID   int
	ManagementChatID    int64
	ManagementMessageID int
	CardState           string
	Decision            string
	ActorUserID         int64
	DecidedAt           *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type ReviewClaim struct {
	Review ReviewItem
	Case   JoinCase
}

func (s *Store) GetManagedChat(ctx context.Context, chatID int64) (ManagedChat, error) {
	var chat ManagedChat
	var enabled, moderationEnabled int
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT chat_id, title, enabled, moderation_enabled, language, COALESCE(management_topic_id, 0),
		       verification_timeout_seconds, reentry_trust_seconds, created_at, updated_at, management_chat_id
		FROM managed_chats WHERE chat_id = ?
	`, chatID).Scan(&chat.ChatID, &chat.Title, &enabled, &moderationEnabled, &chat.Language, &chat.ManagementTopicID,
		&chat.VerificationTimeoutSeconds, &chat.ReentryTrustSeconds, &createdAt, &updatedAt, &chat.ManagementChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedChat{}, ErrManagedChatNotFound
	}
	if err != nil {
		return ManagedChat{}, fmt.Errorf("read managed chat: %w", err)
	}
	chat.Enabled = enabled == 1
	chat.ModerationEnabled = moderationEnabled == 1
	chat.CreatedAt = time.Unix(createdAt, 0).UTC()
	chat.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return chat, nil
}

// SaveManagedChatSetup records an onboarding checkpoint. Callers keep enabled
// false until the management chat has passed its send/edit check.
func (s *Store) SaveManagedChatSetup(ctx context.Context, chat ManagedChat, now time.Time) error {
	if chat.ChatID == 0 || (chat.Language != "zh-CN" && chat.Language != "en") ||
		chat.VerificationTimeoutSeconds < 30 || chat.VerificationTimeoutSeconds > 900 ||
		chat.ReentryTrustSeconds < 0 || chat.ReentryTrustSeconds > 31536000 {
		return ErrJoinCaseState
	}
	enabled := 0
	if chat.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO managed_chats (
			chat_id, title, enabled, language, management_topic_id,
			verification_timeout_seconds, reentry_trust_seconds, created_at, updated_at, management_chat_id
		) VALUES (?, ?, ?, ?, NULLIF(?, 0), ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET
			title = excluded.title,
			enabled = excluded.enabled,
			language = excluded.language,
			management_topic_id = excluded.management_topic_id,
			management_chat_id = excluded.management_chat_id,
			verification_timeout_seconds = excluded.verification_timeout_seconds,
			reentry_trust_seconds = excluded.reentry_trust_seconds,
			updated_at = excluded.updated_at
	`, chat.ChatID, chat.Title, enabled, chat.Language, chat.ManagementTopicID,
		chat.VerificationTimeoutSeconds, chat.ReentryTrustSeconds, now.Unix(), now.Unix(), chat.ManagementChatID)
	if err != nil {
		return fmt.Errorf("save managed chat setup: %w", err)
	}
	return nil
}

func (s *Store) IsTrusted(ctx context.Context, chatID, userID int64, now time.Time) (bool, error) {
	var trusted bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM trusted_users
			WHERE chat_id = ? AND user_id = ? AND trusted_until > ?
		)
	`, chatID, userID, now.Unix()).Scan(&trusted)
	if err != nil {
		return false, fmt.Errorf("read trusted user: %w", err)
	}
	return trusted, nil
}

func (s *Store) UpsertMemberState(ctx context.Context, chatID, userID int64, joinedAt, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO member_states (chat_id, user_id, joined_at, recent_until, normal_message_count)
		VALUES (?, ?, ?, ?, 0)
		ON CONFLICT(chat_id, user_id) DO UPDATE SET
			joined_at = excluded.joined_at,
			recent_until = excluded.recent_until,
			normal_message_count = 0
	`, chatID, userID, joinedAt.Unix(), joinedAt.Add(24*time.Hour).Unix())
	if err != nil {
		return fmt.Errorf("save member state: %w", err)
	}
	_ = now
	return nil
}

func (s *Store) CreateJoinCase(ctx context.Context, item JoinCase, now time.Time) (JoinCase, bool, error) {
	if item.SourceKey == "" || item.UpdateID < 0 || item.ChatID == 0 || item.UserID <= 0 ||
		item.SourceRef == "" || !validJoinPath(item.Path) {
		return JoinCase{}, false, ErrJoinCaseState
	}
	if item.DeadlineAt.IsZero() {
		chat, err := s.GetManagedChat(ctx, item.ChatID)
		if err != nil {
			return JoinCase{}, false, err
		}
		item.DeadlineAt = now.Add(time.Duration(chat.VerificationTimeoutSeconds) * time.Second)
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO join_cases (
			source_key, update_id, path, chat_id, user_id, user_chat_id, source_ref,
			invite_source, user_first_name, user_last_name, username, reason, status,
			action_state, joined_at, created_at, updated_at, deadline_at, prior_permissions, prior_until
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_key) DO NOTHING
	`, item.SourceKey, item.UpdateID, item.Path, item.ChatID, item.UserID, item.UserChatID,
		item.SourceRef, item.InviteSource, item.UserFirstName, item.UserLastName, item.Username,
		cleanReason(item.Reason), JoinInitializing, ActionNone, nullableUnix(item.JoinedAt), now.Unix(), now.Unix(), item.DeadlineAt.Unix(), item.PriorPermissions, item.PriorUntil)
	if err != nil {
		return JoinCase{}, false, fmt.Errorf("create join case: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return JoinCase{}, false, fmt.Errorf("read join case insert count: %w", err)
	}
	created := rows == 1
	stored, err := s.GetJoinCaseBySourceKey(ctx, item.SourceKey)
	return stored, created, err
}

func (s *Store) GetJoinCase(ctx context.Context, id int64) (JoinCase, error) {
	return scanJoinCase(s.db.QueryRowContext(ctx, joinCaseSelect+` WHERE id = ?`, id))
}

func (s *Store) GetJoinCaseBySourceKey(ctx context.Context, sourceKey string) (JoinCase, error) {
	return scanJoinCase(s.db.QueryRowContext(ctx, joinCaseSelect+` WHERE source_key = ?`, sourceKey))
}

func (s *Store) ListOpenJoinCases(ctx context.Context, limit int) ([]JoinCase, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, joinCaseSelect+`
		WHERE status NOT IN (?, ?)
		ORDER BY updated_at, id LIMIT ?
	`, JoinResolved, JoinCancelled, limit)
	if err != nil {
		return nil, fmt.Errorf("list open join cases: %w", err)
	}
	defer rows.Close()
	var items []JoinCase
	for rows.Next() {
		item, err := scanJoinCase(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open join cases: %w", err)
	}
	return items, nil
}

func (s *Store) AttachVerificationSession(ctx context.Context, caseID int64, sessionID string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE join_cases
		SET verification_session_id = ?, updated_at = ?
		WHERE id = ? AND status = ? AND (verification_session_id IS NULL OR verification_session_id = ?)
	`, sessionID, now.Unix(), caseID, JoinInitializing, sessionID)
	if err != nil {
		return fmt.Errorf("attach verification session: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrJoinCaseState
	}
	return nil
}

func (s *Store) MarkCaseVerifying(ctx context.Context, caseID int64, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE join_cases SET status = ?, updated_at = ?
		WHERE id = ? AND status = ? AND verification_session_id IS NOT NULL
	`, JoinVerifying, now.Unix(), caseID, JoinInitializing)
	if err != nil {
		return fmt.Errorf("mark join case verifying: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		item, getErr := s.GetJoinCase(ctx, caseID)
		if getErr == nil && item.Status == JoinVerifying {
			return nil
		}
		return ErrJoinCaseState
	}
	return nil
}

func (s *Store) ScheduleCaseAction(ctx context.Context, caseID int64, kind string, now time.Time) error {
	if kind == "" {
		return ErrJoinCaseState
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE join_cases
		SET action_kind = ?, action_state = ?, updated_at = ?
		WHERE id = ? AND status NOT IN (?, ?)
		  AND NOT (action_kind = ? AND action_state IN (?, ?, ?, ?))
	`, kind, ActionPending, now.Unix(), caseID, JoinResolved, JoinCancelled, kind,
		ActionPending, ActionProcessing, ActionCompleted, ActionFailed)
	if err != nil {
		return fmt.Errorf("schedule join action: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		return nil
	}
	item, getErr := s.GetJoinCase(ctx, caseID)
	if getErr != nil {
		return getErr
	}
	if item.ActionKind == kind && item.ActionState != ActionNone {
		return nil
	}
	return ErrJoinCaseState
}

func (s *Store) ClaimCaseAction(ctx context.Context, caseID int64, retryBefore, now time.Time) (JoinCase, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return JoinCase{}, fmt.Errorf("begin join action transaction: %w", err)
	}
	defer tx.Rollback()
	item, err := scanJoinCase(tx.QueryRowContext(ctx, joinCaseSelect+` WHERE id = ?`, caseID))
	if err != nil {
		return JoinCase{}, err
	}
	claimable := item.ActionState == ActionPending || (item.ActionState == ActionFailed && !item.UpdatedAt.After(retryBefore)) ||
		(item.ActionState == ActionProcessing && !item.UpdatedAt.After(retryBefore))
	if !claimable || item.ActionKind == "" || item.Status == JoinResolved || item.Status == JoinCancelled {
		return JoinCase{}, ErrActionUnavailable
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE join_cases
		SET action_state = ?, action_attempts = action_attempts + 1, updated_at = ?
		WHERE id = ? AND action_kind = ? AND action_state = ? AND updated_at = ?
	`, ActionProcessing, now.Unix(), item.ID, item.ActionKind, item.ActionState, item.UpdatedAt.Unix())
	if err != nil {
		return JoinCase{}, fmt.Errorf("claim join action: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return JoinCase{}, ErrActionUnavailable
	}
	item.ActionState = ActionProcessing
	item.ActionAttempts++
	item.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return JoinCase{}, fmt.Errorf("commit join action claim: %w", err)
	}
	return item, nil
}

func (s *Store) FailCaseAction(ctx context.Context, caseID int64, kind string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin failed action record: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE join_cases SET action_state = ?, updated_at = ?
		WHERE id = ? AND action_kind = ? AND action_state = ?
	`, ActionFailed, now.Unix(), caseID, kind, ActionProcessing)
	if err != nil {
		return fmt.Errorf("fail join action: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events (chat_id, target_user_id, event_type, metadata_json, created_at)
			SELECT chat_id, user_id, 'telegram_action_failed', json_object('action', ?), ?
			FROM join_cases WHERE id = ?
		`, kind, now.Unix(), caseID); err != nil {
			return fmt.Errorf("audit failed action: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failed action record: %w", err)
	}
	return nil
}

// CompleteCaseAction atomically records an external action, optional trust and
// the recent-member state. The expected action prevents late retries from
// completing a newer decision.
func (s *Store) CompleteCaseAction(ctx context.Context, caseID int64, expectedAction, nextStatus string, trustSeconds int, initializeMember bool, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin action completion: %w", err)
	}
	defer tx.Rollback()
	item, err := scanJoinCase(tx.QueryRowContext(ctx, joinCaseSelect+` WHERE id = ?`, caseID))
	if err != nil {
		return err
	}
	if item.ActionKind != expectedAction || item.ActionState != ActionProcessing {
		if item.ActionKind == expectedAction && item.ActionState == ActionCompleted {
			return nil
		}
		return ErrJoinCaseState
	}
	resolvedAt := any(nil)
	if nextStatus == JoinResolved {
		resolvedAt = now.Unix()
	}
	completion, err := tx.ExecContext(ctx, `
		UPDATE join_cases
		SET action_state = ?, status = ?, resolved_at = ?, updated_at = ?
		WHERE id = ? AND action_kind = ? AND action_state = ?
	`, ActionCompleted, nextStatus, resolvedAt, now.Unix(), caseID, expectedAction, ActionProcessing)
	if err != nil {
		return fmt.Errorf("complete join action: %w", err)
	}
	completionRows, _ := completion.RowsAffected()
	if completionRows != 1 {
		return ErrJoinCaseState
	}
	if trustSeconds > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO trusted_users (chat_id, user_id, trusted_until, created_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(chat_id, user_id) DO UPDATE SET trusted_until = excluded.trusted_until
		`, item.ChatID, item.UserID, now.Add(time.Duration(trustSeconds)*time.Second).Unix(), now.Unix()); err != nil {
			return fmt.Errorf("save action trust: %w", err)
		}
	}
	if initializeMember {
		joinedAt := now
		if item.JoinedAt != nil {
			joinedAt = *item.JoinedAt
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO member_states (chat_id, user_id, joined_at, recent_until, normal_message_count)
			VALUES (?, ?, ?, ?, 0)
			ON CONFLICT(chat_id, user_id) DO UPDATE SET
				joined_at = excluded.joined_at,
				recent_until = excluded.recent_until,
				normal_message_count = 0
		`, item.ChatID, item.UserID, joinedAt.Unix(), joinedAt.Add(24*time.Hour).Unix()); err != nil {
			return fmt.Errorf("initialize member state: %w", err)
		}
	}
	if nextStatus == JoinResolved {
		if _, err := tx.ExecContext(ctx, `
			UPDATE review_items
			SET status = ?, card_state = ?, updated_at = ?
			WHERE join_case_id = ? AND status = ?
		`, ReviewCompleted, ActionPending, now.Unix(), caseID, ReviewProcessing); err != nil {
			return fmt.Errorf("complete review decision: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events (chat_id, target_user_id, event_type, metadata_json, created_at)
		VALUES (?, ?, 'telegram_action_completed', json_object('action', ?), ?)
	`, item.ChatID, item.UserID, expectedAction, now.Unix()); err != nil {
		return fmt.Errorf("audit completed action: %w", err)
	}
	if item.Reason == RuleBlocklist && expectedAction == "ban" {
		if err := insertModerationActionTx(ctx, tx, ModerationAction{
			IdempotencyKey: fmt.Sprintf("join-log:%d", item.ID), ChatID: item.ChatID,
			TargetKind: ModerationTargetUser, TargetID: item.UserID,
			ActionKind: ModerationLog, Reason: RuleBlocklist + ":completed",
		}, now); err != nil {
			return err
		}
	}
	if nextStatus == JoinResolved {
		_, err = tx.ExecContext(ctx, `INSERT INTO review_items
 (chat_id,user_id,status,created_at,updated_at,join_case_id,public_id,reason,deadline_at,card_state,decision,management_chat_id,decided_at)
 SELECT j.chat_id,j.user_id,'completed',?,?,j.id,lower(hex(randomblob(12))),j.reason,j.deadline_at,'pending',j.action_kind,c.management_chat_id,?
 FROM join_cases j JOIN managed_chats c ON c.chat_id=j.chat_id
 WHERE j.id=? AND c.management_chat_id<>0 ON CONFLICT DO NOTHING`, now.Unix(), now.Unix(), now.Unix(), caseID)
		if err != nil {
			return fmt.Errorf("create completion log: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit action completion: %w", err)
	}
	return nil
}

func (s *Store) EnterReview(ctx context.Context, caseID int64, reason string, queueGuard bool, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin review transition: %w", err)
	}
	defer tx.Rollback()
	item, err := scanJoinCase(tx.QueryRowContext(ctx, joinCaseSelect+` WHERE id = ?`, caseID))
	if err != nil {
		return err
	}
	if item.Status == JoinResolved || item.Status == JoinCancelled {
		return ErrJoinCaseState
	}
	actionKind, actionState := "", ActionNone
	if queueGuard && item.ActionKind == "guard_queue" && item.ActionState == ActionCompleted {
		actionKind, actionState = item.ActionKind, item.ActionState
	} else if queueGuard {
		actionKind, actionState = "guard_queue", ActionPending
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE join_cases
		SET status = ?, reason = ?, action_kind = ?, action_state = ?, updated_at = ?
		WHERE id = ?
	`, JoinReviewPending, reason, actionKind, actionState, now.Unix(), caseID); err != nil {
		return fmt.Errorf("enter review: %w", err)
	}
	if item.VerificationSessionID != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE verification_sessions
			SET status = ?, challenge_secret = NULL, updated_at = ?
			WHERE id = ? AND status IN (?, ?)
		`, VerificationCancelled, now.Unix(), item.VerificationSessionID, VerificationPending, VerificationChallengeIssued); err != nil {
			return fmt.Errorf("cancel unreachable verification: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events (chat_id, target_user_id, event_type, metadata_json, created_at)
		VALUES (?, ?, 'review_required', json_object('reason', ?), ?)
	`, item.ChatID, item.UserID, reason, now.Unix()); err != nil {
		return fmt.Errorf("audit review transition: %w", err)
	}
	return tx.Commit()
}

func (s *Store) EnsureReview(ctx context.Context, caseID int64, publicID string, deadline, now time.Time) (ReviewItem, bool, error) {
	if publicID == "" {
		return ReviewItem{}, false, ErrJoinCaseState
	}
	item, err := s.GetJoinCase(ctx, caseID)
	if err != nil {
		return ReviewItem{}, false, err
	}
	if item.Status != JoinReviewPending {
		return ReviewItem{}, false, ErrJoinCaseState
	}
	chat, err := s.GetManagedChat(ctx, item.ChatID)
	if err != nil {
		return ReviewItem{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewItem{}, false, fmt.Errorf("begin review creation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO review_items (
			chat_id, user_id, verification_session_id, status, created_at, updated_at,
			join_case_id, public_id, reason, deadline_at, management_topic_id, card_state, management_chat_id
		) VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`, item.ChatID, item.UserID, item.VerificationSessionID, ReviewPending, now.Unix(), now.Unix(),
		item.ID, publicID, item.Reason, deadline.Unix(), 0, ActionPending, chat.ManagementChatID)
	if err != nil {
		return ReviewItem{}, false, fmt.Errorf("create review item: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events (chat_id, target_user_id, event_type, metadata_json, created_at)
			VALUES (?, ?, 'review_created', json_object('reason', ?), ?)
		`, item.ChatID, item.UserID, item.Reason, now.Unix()); err != nil {
			return ReviewItem{}, false, fmt.Errorf("audit review creation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return ReviewItem{}, false, fmt.Errorf("commit review creation: %w", err)
	}
	review, err := s.GetReviewByCase(ctx, caseID)
	return review, rows == 1, err
}

func (s *Store) GetReviewByCase(ctx context.Context, caseID int64) (ReviewItem, error) {
	return scanReview(s.db.QueryRowContext(ctx, reviewSelect+` WHERE join_case_id = ?`, caseID))
}

func (s *Store) GetReviewByPublicID(ctx context.Context, publicID string) (ReviewItem, error) {
	return scanReview(s.db.QueryRowContext(ctx, reviewSelect+` WHERE public_id = ?`, publicID))
}

func (s *Store) ListClaimableReviewCards(ctx context.Context, retryBefore time.Time, limit int) ([]ReviewClaim, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, reviewSelect+`
		WHERE (card_state IN (?, ?) OR (card_state = ? AND updated_at <= ?))
		  AND ((status = ? AND management_message_id IS NULL) OR status = ?)
		ORDER BY updated_at, id LIMIT ?
	`, ActionPending, ActionFailed, ActionProcessing, retryBefore.Unix(), ReviewPending, ReviewCompleted, limit)
	if err != nil {
		return nil, fmt.Errorf("list review cards: %w", err)
	}
	defer rows.Close()
	var reviews []ReviewItem
	for rows.Next() {
		review, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, review)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review cards: %w", err)
	}
	claims := make([]ReviewClaim, 0, len(reviews))
	for _, review := range reviews {
		item, err := s.GetJoinCase(ctx, review.JoinCaseID)
		if err != nil {
			return nil, err
		}
		claims = append(claims, ReviewClaim{Review: review, Case: item})
	}
	return claims, nil
}

func (s *Store) ClaimReviewCard(ctx context.Context, reviewID int64, retryBefore, now time.Time) (ReviewItem, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE review_items SET card_state = ?, updated_at = ?
		WHERE id = ? AND (
			card_state IN (?, ?) OR (card_state = ? AND updated_at <= ?)
		)
	`, ActionProcessing, now.Unix(), reviewID, ActionPending, ActionFailed, ActionProcessing, retryBefore.Unix())
	if err != nil {
		return ReviewItem{}, fmt.Errorf("claim review card: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ReviewItem{}, ErrActionUnavailable
	}
	return scanReview(s.db.QueryRowContext(ctx, reviewSelect+` WHERE id = ?`, reviewID))
}

func (s *Store) CompleteReviewCard(ctx context.Context, reviewID int64, messageID int, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin review card completion: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE review_items
		SET management_message_id = CASE WHEN management_message_id IS NULL THEN NULLIF(?, 0) ELSE management_message_id END,
		    card_state = ?, updated_at = ?
		WHERE id = ? AND card_state = ?
	`, messageID, ActionCompleted, now.Unix(), reviewID, ActionProcessing)
	if err != nil {
		return fmt.Errorf("complete review card: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrJoinCaseState
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events (chat_id, target_user_id, event_type, metadata_json, created_at)
		SELECT chat_id, user_id, 'review_card_synced', '{}', ?
		FROM review_items WHERE id = ?
	`, now.Unix(), reviewID); err != nil {
		return fmt.Errorf("audit review card: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit review card completion: %w", err)
	}
	return nil
}

func (s *Store) FailReviewCard(ctx context.Context, reviewID int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE review_items SET card_state = ?, updated_at = ?
		WHERE id = ? AND card_state = ?
	`, ActionFailed, now.Unix(), reviewID, ActionProcessing)
	if err != nil {
		return fmt.Errorf("fail review card: %w", err)
	}
	return nil
}

func (s *Store) ClaimReviewDecision(ctx context.Context, publicID, decision string, actorUserID int64, now time.Time) (ReviewClaim, error) {
	if !validReviewDecision(decision) || actorUserID <= 0 {
		return ReviewClaim{}, ErrReviewProcessed
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewClaim{}, fmt.Errorf("begin review decision: %w", err)
	}
	defer tx.Rollback()
	review, err := scanReview(tx.QueryRowContext(ctx, reviewSelect+` WHERE public_id = ?`, publicID))
	if err != nil {
		return ReviewClaim{}, err
	}
	if review.Status != ReviewPending || !now.Before(review.DeadlineAt) {
		return ReviewClaim{}, ErrReviewProcessed
	}
	item, err := scanJoinCase(tx.QueryRowContext(ctx, joinCaseSelect+` WHERE id = ?`, review.JoinCaseID))
	if err != nil {
		return ReviewClaim{}, err
	}
	if item.Path == JoinPathGuard && (item.ActionKind != "guard_queue" || item.ActionState != ActionCompleted) {
		return ReviewClaim{}, ErrActionUnavailable
	}
	action := decisionAction(item.Path, decision)
	if action == "" {
		return ReviewClaim{}, ErrJoinCaseState
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE review_items
		SET status = ?, decision = ?, actor_user_id = ?, decided_at = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, ReviewProcessing, decision, actorUserID, now.Unix(), now.Unix(), review.ID, ReviewPending)
	if err != nil {
		return ReviewClaim{}, fmt.Errorf("claim review decision: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ReviewClaim{}, ErrReviewProcessed
	}
	caseResult, err := tx.ExecContext(ctx, `
		UPDATE join_cases SET action_kind = ?, action_state = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, action, ActionPending, now.Unix(), item.ID, JoinReviewPending)
	if err != nil {
		return ReviewClaim{}, fmt.Errorf("schedule review action: %w", err)
	}
	caseRows, _ := caseResult.RowsAffected()
	if caseRows != 1 {
		return ReviewClaim{}, ErrJoinCaseState
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events (chat_id, actor_user_id, target_user_id, event_type, metadata_json, created_at)
		VALUES (?, ?, ?, 'review_decision', json_object('decision', ?), ?)
	`, item.ChatID, actorUserID, item.UserID, decision, now.Unix()); err != nil {
		return ReviewClaim{}, fmt.Errorf("audit review decision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ReviewClaim{}, fmt.Errorf("commit review decision: %w", err)
	}
	review.Status = ReviewProcessing
	review.Decision = decision
	review.ActorUserID = actorUserID
	decidedAt := now
	review.DecidedAt = &decidedAt
	item.ActionKind = action
	item.ActionState = ActionPending
	return ReviewClaim{Review: review, Case: item}, nil
}

func (s *Store) ClaimExpiredReviews(ctx context.Context, now time.Time, limit int) ([]ReviewClaim, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin review timeout transaction: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, reviewSelect+`
		WHERE status = ? AND deadline_at <= ?
		ORDER BY deadline_at, id LIMIT ?
	`, ReviewPending, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("list expired reviews: %w", err)
	}
	var reviews []ReviewItem
	for rows.Next() {
		review, scanErr := scanReview(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		reviews = append(reviews, review)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close expired reviews: %w", err)
	}
	claims := make([]ReviewClaim, 0, len(reviews))
	for _, review := range reviews {
		item, err := scanJoinCase(tx.QueryRowContext(ctx, joinCaseSelect+` WHERE id = ?`, review.JoinCaseID))
		if err != nil {
			return nil, err
		}
		action := decisionAction(item.Path, ReviewDecisionTime)
		result, err := tx.ExecContext(ctx, `
			UPDATE review_items
			SET status = ?, decision = ?, decided_at = ?, updated_at = ?
			WHERE id = ? AND status = ?
		`, ReviewProcessing, ReviewDecisionTime, now.Unix(), now.Unix(), review.ID, ReviewPending)
		if err != nil {
			return nil, fmt.Errorf("claim expired review: %w", err)
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			continue
		}
		caseResult, err := tx.ExecContext(ctx, `
			UPDATE join_cases SET action_kind = ?, action_state = ?, updated_at = ?
			WHERE id = ? AND status = ?
		`, action, ActionPending, now.Unix(), item.ID, JoinReviewPending)
		if err != nil {
			return nil, fmt.Errorf("schedule review timeout action: %w", err)
		}
		caseRows, _ := caseResult.RowsAffected()
		if caseRows != 1 {
			return nil, ErrJoinCaseState
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events (chat_id, target_user_id, event_type, metadata_json, created_at)
			VALUES (?, ?, 'review_timeout', '{}', ?)
		`, item.ChatID, item.UserID, now.Unix()); err != nil {
			return nil, fmt.Errorf("audit review timeout: %w", err)
		}
		review.Status = ReviewProcessing
		review.Decision = ReviewDecisionTime
		decidedAt := now
		review.DecidedAt = &decidedAt
		item.ActionKind = action
		item.ActionState = ActionPending
		claims = append(claims, ReviewClaim{Review: review, Case: item})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit review timeouts: %w", err)
	}
	return claims, nil
}

func (s *Store) ExpireDueVerificationSessions(ctx context.Context, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE verification_sessions
		SET status = ?, challenge_secret = NULL, updated_at = ?
		WHERE status IN (?, ?) AND expires_at <= ?
	`, VerificationExpired, now.Unix(), VerificationPending, VerificationChallengeIssued, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("expire verification sessions: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read expired verification count: %w", err)
	}
	return count, nil
}

func (s *Store) HasResolvedApplication(ctx context.Context, chatID, userID int64, since time.Time) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM join_cases
			WHERE chat_id = ? AND user_id = ? AND path IN (?, ?)
			  AND status = ? AND resolved_at >= ?
			  AND action_kind IN ('approve_request', 'guard_approve')
		)
	`, chatID, userID, JoinPathGuard, JoinPathRequest, JoinResolved, since.Unix()).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("check resolved join application: %w", err)
	}
	return found, nil
}

// UseResolvedApplication consumes a successful application and schedules the
// corresponding member event in one transaction, including restart recovery.
func (s *Store) UseResolvedApplication(ctx context.Context, caseID int64, since, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	item, err := scanJoinCase(tx.QueryRowContext(ctx, joinCaseSelect+` WHERE id=?`, caseID))
	if err != nil {
		return false, err
	}
	if item.Path != JoinPathDirect || item.Status != JoinInitializing {
		return false, ErrJoinCaseState
	}
	if item.ActionKind == "record_member" {
		return true, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE join_cases SET member_event_consumed=1 WHERE id=(
 SELECT id FROM join_cases WHERE chat_id=? AND user_id=? AND path IN ('guard','join_request') AND status='resolved'
 AND resolved_at>=? AND action_kind IN ('approve_request','guard_approve') AND member_event_consumed=0 ORDER BY id DESC LIMIT 1
 ) AND member_event_consumed=0`, item.ChatID, item.UserID, since.Unix())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE join_cases SET action_kind='record_member',action_state='pending',updated_at=? WHERE id=?`, now.Unix(), caseID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) CancelDirectJoin(ctx context.Context, chatID, userID int64, now time.Time) error {
	var caseID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM join_cases WHERE chat_id=? AND user_id=? AND path=? AND status IN (?,?,?) ORDER BY id DESC LIMIT 1`, chatID, userID, JoinPathDirect, JoinInitializing, JoinVerifying, JoinReviewPending).Scan(&caseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.CancelJoinCase(ctx, caseID, now)
}

func (s *Store) CancelJoinCase(ctx context.Context, caseID int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin direct cancellation: %w", err)
	}
	defer tx.Rollback()
	var sessionID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT verification_session_id FROM join_cases WHERE id=? AND status NOT IN (?,?)`, caseID, JoinResolved, JoinCancelled).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find direct join case: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE join_cases
		SET status = ?, action_state = ?, resolved_at = ?, updated_at = ? WHERE id = ?
	`, JoinCancelled, ActionNone, now.Unix(), now.Unix(), caseID); err != nil {
		return fmt.Errorf("cancel direct join case: %w", err)
	}
	if sessionID.Valid {
		if _, err := tx.ExecContext(ctx, `
			UPDATE verification_sessions
			SET status = ?, challenge_secret = NULL, updated_at = ?
			WHERE id = ? AND status IN (?, ?)
		`, VerificationCancelled, now.Unix(), sessionID.String, VerificationPending, VerificationChallengeIssued); err != nil {
			return fmt.Errorf("cancel direct verification: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE review_items
		SET status = ?, decision = 'cancelled', decided_at = ?, card_state = ?, updated_at = ?
		WHERE join_case_id = ? AND status IN (?, ?)
	`, ReviewCompleted, now.Unix(), ActionPending, now.Unix(), caseID, ReviewPending, ReviewProcessing); err != nil {
		return fmt.Errorf("close direct review: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(chat_id,target_user_id,event_type,metadata_json,created_at)
 SELECT chat_id,user_id,'join_cancelled','{}',? FROM join_cases WHERE id=?`, now.Unix(), caseID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AddAuditEvent(ctx context.Context, chatID int64, actorUserID, targetUserID *int64, eventType, metadataJSON string, now time.Time) error {
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_events (chat_id, actor_user_id, target_user_id, event_type, metadata_json, created_at)
		VALUES (?, ?, ?, ?, json(?), ?)
	`, chatID, actorUserID, targetUserID, eventType, metadataJSON, now.Unix())
	if err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

const joinCaseSelect = `
	SELECT id, source_key, update_id, path, chat_id, user_id, user_chat_id, source_ref,
	       invite_source, user_first_name, user_last_name, username,
	       COALESCE(verification_session_id, ''), status, reason, action_kind,
	       action_state, action_attempts, joined_at, resolved_at, created_at, updated_at, deadline_at, prior_permissions, prior_until
	FROM join_cases`

const reviewSelect = `
	SELECT id, public_id, COALESCE(join_case_id, 0), chat_id, user_id,
	       COALESCE(verification_session_id, ''), status, reason,
	       COALESCE(deadline_at, 0), COALESCE(management_topic_id, 0),
	       COALESCE(management_message_id, 0), card_state, decision,
	       COALESCE(actor_user_id, 0), decided_at, created_at, updated_at, management_chat_id
	FROM review_items`

func scanJoinCase(row rowScanner) (JoinCase, error) {
	var item JoinCase
	var joinedAt, resolvedAt sql.NullInt64
	var deadlineAt int64
	var createdAt, updatedAt int64
	err := row.Scan(&item.ID, &item.SourceKey, &item.UpdateID, &item.Path, &item.ChatID,
		&item.UserID, &item.UserChatID, &item.SourceRef, &item.InviteSource,
		&item.UserFirstName, &item.UserLastName, &item.Username,
		&item.VerificationSessionID, &item.Status, &item.Reason, &item.ActionKind,
		&item.ActionState, &item.ActionAttempts, &joinedAt, &resolvedAt, &createdAt, &updatedAt, &deadlineAt, &item.PriorPermissions, &item.PriorUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return JoinCase{}, ErrJoinCaseNotFound
	}
	if err != nil {
		return JoinCase{}, fmt.Errorf("read join case: %w", err)
	}
	item.DeadlineAt = time.Unix(deadlineAt, 0).UTC()
	item.JoinedAt = timePointer(joinedAt)
	item.ResolvedAt = timePointer(resolvedAt)
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return item, nil
}

func scanReview(row rowScanner) (ReviewItem, error) {
	var item ReviewItem
	var deadlineAt, createdAt, updatedAt int64
	var decidedAt sql.NullInt64
	err := row.Scan(&item.ID, &item.PublicID, &item.JoinCaseID, &item.ChatID, &item.UserID,
		&item.VerificationSession, &item.Status, &item.Reason, &deadlineAt,
		&item.ManagementTopicID, &item.ManagementMessageID, &item.CardState,
		&item.Decision, &item.ActorUserID, &decidedAt, &createdAt, &updatedAt, &item.ManagementChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewItem{}, ErrReviewNotFound
	}
	if err != nil {
		return ReviewItem{}, fmt.Errorf("read review item: %w", err)
	}
	item.DeadlineAt = time.Unix(deadlineAt, 0).UTC()
	item.DecidedAt = timePointer(decidedAt)
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return item, nil
}

func nullableUnix(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Unix()
}

func timePointer(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	parsed := time.Unix(value.Int64, 0).UTC()
	return &parsed
}

func validJoinPath(path string) bool {
	return path == JoinPathGuard || path == JoinPathRequest || path == JoinPathDirect
}

func validReviewDecision(decision string) bool {
	return decision == ReviewDecisionOnce || decision == ReviewDecisionTrust ||
		decision == ReviewDecisionDeny || decision == ReviewDecisionBan
}

func decisionAction(path, decision string) string {
	switch decision {
	case ReviewDecisionOnce, ReviewDecisionTrust:
		if path == JoinPathDirect {
			return "restore"
		}
		return "approve_request"
	case ReviewDecisionDeny, ReviewDecisionTime:
		if path == JoinPathDirect {
			return "kick"
		}
		return "decline_request"
	case ReviewDecisionBan:
		return "ban"
	default:
		return ""
	}
}
