package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrVerificationNotFound     = errors.New("verification session not found")
	ErrVerificationExpired      = errors.New("verification session expired")
	ErrVerificationStale        = errors.New("verification challenge is stale")
	ErrVerificationCompleted    = errors.New("verification session already completed")
	ErrVerificationReview       = errors.New("verification session is awaiting review")
	ErrVerificationCancelled    = errors.New("verification session was cancelled")
	ErrVerificationInvalidState = errors.New("invalid verification state transition")
)

const (
	VerificationPending         = "pending"
	VerificationChallengeIssued = "challenge_issued"
	VerificationPassed          = "challenge_passed"
	VerificationAwaitingReview  = "awaiting_review"
	VerificationExpired         = "expired"
	VerificationCancelled       = "cancelled"
)

type VerificationSession struct {
	ID              string
	ChatID          int64
	UserID          int64
	SourceKind      string
	SourceRef       string
	Language        string
	Status          string
	Attempts        int
	Generation      int
	ChallengeSecret []byte
	ExpiresAt       time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ConsumedAt      *time.Time
}

type VerificationSubmitResult struct {
	Session VerificationSession
	Correct bool
}

// SaveManagedChat persists a managed chat for future lifecycle stages. Phase 2
// uses it only from tests and internal callers; no public HTTP route exposes it.
func (s *Store) SaveManagedChat(ctx context.Context, chatID int64, title, language string, enabled bool, now time.Time) error {
	enabledValue := 0
	if enabled {
		enabledValue = 1
	}
	nowUnix := now.Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO managed_chats (chat_id, title, language, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET
			title = excluded.title,
			language = excluded.language,
			enabled = excluded.enabled,
			updated_at = excluded.updated_at
	`, chatID, title, language, enabledValue, nowUnix, nowUnix)
	if err != nil {
		return fmt.Errorf("save managed chat: %w", err)
	}
	return nil
}

func (s *Store) CreateVerificationSession(ctx context.Context, session VerificationSession) error {
	if session.ID == "" || session.ChatID == 0 || session.UserID <= 0 || session.SourceKind == "" || session.SourceRef == "" ||
		session.Status != VerificationChallengeIssued || session.Attempts != 0 || session.Generation != 1 ||
		len(session.ChallengeSecret) < 16 || session.CreatedAt.IsZero() || session.UpdatedAt.IsZero() ||
		!session.ExpiresAt.After(session.CreatedAt) || session.ConsumedAt != nil {
		return ErrVerificationInvalidState
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO verification_sessions (
			id, chat_id, user_id, status, attempts, expires_at, created_at, updated_at,
			source_kind, source_ref, language, generation, challenge_secret, consumed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, session.ID, session.ChatID, session.UserID, session.Status, session.Attempts,
		session.ExpiresAt.Unix(), session.CreatedAt.Unix(), session.UpdatedAt.Unix(),
		session.SourceKind, session.SourceRef, session.Language, session.Generation, session.ChallengeSecret)
	if err != nil {
		return fmt.Errorf("create verification session: %w", err)
	}
	return nil
}

func (s *Store) GetVerificationSession(ctx context.Context, id string) (VerificationSession, error) {
	return scanVerificationSession(s.db.QueryRowContext(ctx, verificationSessionSelect+` WHERE id = ?`, id))
}

func (s *Store) GetVerificationSessionBySource(ctx context.Context, chatID int64, sourceKind, sourceRef string) (VerificationSession, error) {
	return scanVerificationSession(s.db.QueryRowContext(ctx, verificationSessionSelect+`
		WHERE chat_id = ? AND source_kind = ? AND source_ref = ?
	`, chatID, sourceKind, sourceRef))
}

// GetActiveVerificationSession returns a usable challenge and atomically marks
// it expired when the fixed deadline has elapsed.
func (s *Store) GetActiveVerificationSession(ctx context.Context, id string, now time.Time) (VerificationSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VerificationSession{}, fmt.Errorf("begin verification transaction: %w", err)
	}
	defer tx.Rollback()

	session, err := scanVerificationSession(tx.QueryRowContext(ctx, verificationSessionSelect+` WHERE id = ?`, id))
	if err != nil {
		return VerificationSession{}, err
	}
	if !now.Before(session.ExpiresAt) && isActiveVerificationStatus(session.Status) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE verification_sessions
			SET status = ?, challenge_secret = NULL, updated_at = ?
			WHERE id = ? AND status IN (?, ?)
		`, VerificationExpired, now.Unix(), id, VerificationPending, VerificationChallengeIssued); err != nil {
			return VerificationSession{}, fmt.Errorf("expire verification session: %w", err)
		}
		session.Status = VerificationExpired
		session.ChallengeSecret = nil
		session.UpdatedAt = now
	}
	if err := verificationStatusError(session.Status); err != nil {
		if commitErr := tx.Commit(); commitErr != nil {
			return VerificationSession{}, fmt.Errorf("commit verification transaction: %w", commitErr)
		}
		return VerificationSession{}, err
	}
	if session.Status != VerificationChallengeIssued || session.Generation < 1 || len(session.ChallengeSecret) == 0 {
		return VerificationSession{}, ErrVerificationInvalidState
	}
	if err := tx.Commit(); err != nil {
		return VerificationSession{}, fmt.Errorf("commit verification transaction: %w", err)
	}
	return session, nil
}

// SubmitVerification performs the answer transition in one SQLite transaction.
// A failed answer either rotates the challenge or, on the final attempt, moves
// the session to awaiting_review. The original expires_at is never changed.
func (s *Store) SubmitVerification(ctx context.Context, id string, generation int, correct bool, replacementSecret []byte, maxAttempts int, now time.Time) (VerificationSubmitResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VerificationSubmitResult{}, fmt.Errorf("begin verification transaction: %w", err)
	}
	defer tx.Rollback()

	session, err := scanVerificationSession(tx.QueryRowContext(ctx, verificationSessionSelect+` WHERE id = ?`, id))
	if err != nil {
		return VerificationSubmitResult{}, err
	}
	if !now.Before(session.ExpiresAt) && isActiveVerificationStatus(session.Status) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE verification_sessions
			SET status = ?, challenge_secret = NULL, updated_at = ?
			WHERE id = ? AND status IN (?, ?)
		`, VerificationExpired, now.Unix(), id, VerificationPending, VerificationChallengeIssued); err != nil {
			return VerificationSubmitResult{}, fmt.Errorf("expire verification session: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return VerificationSubmitResult{}, fmt.Errorf("commit verification transaction: %w", err)
		}
		return VerificationSubmitResult{}, ErrVerificationExpired
	}
	if err := verificationStatusError(session.Status); err != nil {
		return VerificationSubmitResult{}, err
	}
	if session.Status != VerificationChallengeIssued || session.Generation != generation {
		if session.Status == VerificationChallengeIssued {
			return VerificationSubmitResult{}, ErrVerificationStale
		}
		return VerificationSubmitResult{}, ErrVerificationInvalidState
	}
	if maxAttempts < 1 || session.Attempts >= maxAttempts {
		return VerificationSubmitResult{}, ErrVerificationInvalidState
	}

	session.Attempts++
	session.UpdatedAt = now
	var updateResult sql.Result
	switch {
	case correct:
		session.Status = VerificationPassed
		session.ChallengeSecret = nil
		consumedAt := now
		session.ConsumedAt = &consumedAt
		updateResult, err = tx.ExecContext(ctx, `
			UPDATE verification_sessions
			SET status = ?, attempts = ?, challenge_secret = NULL, consumed_at = ?, updated_at = ?
			WHERE id = ? AND status = ? AND generation = ?
		`, session.Status, session.Attempts, now.Unix(), now.Unix(), id, VerificationChallengeIssued, generation)
	case session.Attempts >= maxAttempts:
		session.Status = VerificationAwaitingReview
		session.ChallengeSecret = nil
		updateResult, err = tx.ExecContext(ctx, `
			UPDATE verification_sessions
			SET status = ?, attempts = ?, challenge_secret = NULL, updated_at = ?
			WHERE id = ? AND status = ? AND generation = ?
		`, session.Status, session.Attempts, now.Unix(), id, VerificationChallengeIssued, generation)
	default:
		if len(replacementSecret) < 16 {
			return VerificationSubmitResult{}, ErrVerificationInvalidState
		}
		session.Generation++
		session.ChallengeSecret = append([]byte(nil), replacementSecret...)
		updateResult, err = tx.ExecContext(ctx, `
			UPDATE verification_sessions
			SET attempts = ?, generation = ?, challenge_secret = ?, updated_at = ?
			WHERE id = ? AND status = ? AND generation = ?
		`, session.Attempts, session.Generation, replacementSecret, now.Unix(), id, VerificationChallengeIssued, generation)
	}
	if err != nil {
		return VerificationSubmitResult{}, fmt.Errorf("update verification session: %w", err)
	}
	rows, err := updateResult.RowsAffected()
	if err != nil {
		return VerificationSubmitResult{}, fmt.Errorf("read verification update count: %w", err)
	}
	if rows != 1 {
		return VerificationSubmitResult{}, ErrVerificationStale
	}
	if err := tx.Commit(); err != nil {
		return VerificationSubmitResult{}, fmt.Errorf("commit verification transaction: %w", err)
	}
	return VerificationSubmitResult{Session: session, Correct: correct}, nil
}

func (s *Store) CancelVerificationSession(ctx context.Context, id string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE verification_sessions
		SET status = ?, challenge_secret = NULL, updated_at = ?
		WHERE id = ? AND status IN (?, ?)
	`, VerificationCancelled, now.Unix(), id, VerificationPending, VerificationChallengeIssued)
	if err != nil {
		return fmt.Errorf("cancel verification session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read cancelled row count: %w", err)
	}
	if rows == 1 {
		return nil
	}
	session, err := s.GetVerificationSession(ctx, id)
	if err != nil {
		return err
	}
	if statusErr := verificationStatusError(session.Status); statusErr != nil {
		return statusErr
	}
	return ErrVerificationInvalidState
}

const verificationSessionSelect = `
	SELECT id, chat_id, user_id, source_kind, source_ref, language, status,
	       attempts, generation, challenge_secret, expires_at, created_at, updated_at, consumed_at
	FROM verification_sessions`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVerificationSession(row rowScanner) (VerificationSession, error) {
	var session VerificationSession
	var expiresAt, createdAt, updatedAt int64
	var consumedAt sql.NullInt64
	if err := row.Scan(
		&session.ID, &session.ChatID, &session.UserID, &session.SourceKind, &session.SourceRef,
		&session.Language, &session.Status, &session.Attempts, &session.Generation,
		&session.ChallengeSecret, &expiresAt, &createdAt, &updatedAt, &consumedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return VerificationSession{}, ErrVerificationNotFound
		}
		return VerificationSession{}, fmt.Errorf("read verification session: %w", err)
	}
	session.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	session.CreatedAt = time.Unix(createdAt, 0).UTC()
	session.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if consumedAt.Valid {
		value := time.Unix(consumedAt.Int64, 0).UTC()
		session.ConsumedAt = &value
	}
	session.ChallengeSecret = append([]byte(nil), session.ChallengeSecret...)
	return session, nil
}

func isActiveVerificationStatus(status string) bool {
	return status == VerificationPending || status == VerificationChallengeIssued
}

func verificationStatusError(status string) error {
	switch status {
	case VerificationPending, VerificationChallengeIssued:
		return nil
	case VerificationPassed:
		return ErrVerificationCompleted
	case VerificationAwaitingReview:
		return ErrVerificationReview
	case VerificationExpired:
		return ErrVerificationExpired
	case VerificationCancelled:
		return ErrVerificationCancelled
	default:
		return ErrVerificationInvalidState
	}
}
