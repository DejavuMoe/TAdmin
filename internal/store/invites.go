package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	InvitePending           = "pending"
	InviteActive            = "active"
	InviteRevocationPending = "revocation_pending"
	InviteRevoked           = "revoked"
	InviteFailed            = "failed"
	InviteIndeterminate     = "indeterminate"

	InviteActionCreate = "create"
	InviteActionRevoke = "revoke"
	InviteActionNone   = "none"

	InviteActionPending    = "pending"
	InviteActionProcessing = "processing"
	InviteActionCompleted  = "completed"
	InviteActionFailed     = "failed"
)

var (
	ErrInviteNotFound = errors.New("invite link record not found")
	ErrInviteState    = errors.New("invite link is not available for this operation")
	ErrInviteInput    = errors.New("invalid invite link record")
)

type InviteLink struct {
	ID                 int64
	PublicID           string
	CreateOperationKey string
	RevokeOperationKey string
	ChatID             int64
	DisplayName        string
	TelegramName       string
	CreatorUserID      int64
	RevokedByUserID    int64
	Status             string
	EncryptedLink      []byte
	LinkNonce          []byte
	LinkFingerprint    []byte
	ActionKind         string
	ActionState        string
	Attempts           int
	LastResult         string
	RevokedAt          *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type InviteSummary struct {
	PublicID    string
	ChatID      int64
	DisplayName string
	Status      string
	LastResult  string
	Attempts    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	RevokedAt   *time.Time
}

func (s *Store) CreateInviteLinkRecord(ctx context.Context, record InviteLink, now time.Time) (InviteLink, bool, error) {
	if !validNewInvite(record) || now.IsZero() {
		return InviteLink{}, false, ErrInviteInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InviteLink{}, false, fmt.Errorf("begin invite record creation: %w", err)
	}
	defer tx.Rollback()

	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM managed_chats WHERE chat_id = ?`, record.ChatID).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return InviteLink{}, false, ErrManagedChatNotFound
		}
		return InviteLink{}, false, fmt.Errorf("read invite managed chat: %w", err)
	}
	if enabled != 1 {
		return InviteLink{}, false, ErrManagedChatNotFound
	}

	existing, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelect+` WHERE create_operation_key = ?`, record.CreateOperationKey))
	if err == nil {
		if err := tx.Commit(); err != nil {
			return InviteLink{}, false, fmt.Errorf("commit duplicate invite lookup: %w", err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, ErrInviteNotFound) {
		return InviteLink{}, false, err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO invite_links (
			public_id, create_operation_key, chat_id, display_name, telegram_name,
			creator_user_id, status, action_kind, action_state, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.PublicID, record.CreateOperationKey, record.ChatID, record.DisplayName,
		record.TelegramName, record.CreatorUserID, InvitePending, InviteActionCreate,
		InviteActionPending, now.Unix(), now.Unix())
	if err != nil {
		return InviteLink{}, false, fmt.Errorf("create invite record: %w", err)
	}
	created, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelect+` WHERE public_id = ?`, record.PublicID))
	if err != nil {
		return InviteLink{}, false, err
	}
	if err := insertInviteAuditTx(ctx, tx, created, "invite_create_requested", "pending", now); err != nil {
		return InviteLink{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return InviteLink{}, false, fmt.Errorf("commit invite record creation: %w", err)
	}
	return created, true, nil
}

func (s *Store) FailInviteBeforeCreate(ctx context.Context, publicID, result string, now time.Time) error {
	return s.finishInviteCreate(ctx, publicID, InvitePending, InviteActionPending, InviteFailed, result, now)
}

func (s *Store) ClaimInviteCreate(ctx context.Context, publicID string, now time.Time) (InviteLink, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InviteLink{}, fmt.Errorf("begin invite create claim: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE invite_links
		SET action_state = ?, attempts = attempts + 1, updated_at = ?
		WHERE public_id = ? AND status = ? AND action_kind = ? AND action_state = ?
	`, InviteActionProcessing, now.Unix(), publicID, InvitePending, InviteActionCreate, InviteActionPending)
	if err != nil {
		return InviteLink{}, fmt.Errorf("claim invite creation: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return InviteLink{}, ErrInviteState
	}
	record, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelect+` WHERE public_id = ?`, publicID))
	if err != nil {
		return InviteLink{}, err
	}
	if err := tx.Commit(); err != nil {
		return InviteLink{}, fmt.Errorf("commit invite create claim: %w", err)
	}
	return record, nil
}

func (s *Store) ActivateInviteLink(ctx context.Context, publicID string, encryptedLink, nonce, fingerprint []byte, now time.Time) error {
	if len(encryptedLink) == 0 || len(nonce) == 0 || len(fingerprint) == 0 {
		return ErrInviteInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin invite activation: %w", err)
	}
	defer tx.Rollback()
	record, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelect+` WHERE public_id = ?`, publicID))
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE invite_links
		SET status = ?, encrypted_link = ?, link_nonce = ?, link_fingerprint = ?,
		    action_state = ?, last_result = 'created', updated_at = ?
		WHERE id = ? AND status = ? AND action_kind = ? AND action_state = ?
	`, InviteActive, encryptedLink, nonce, fingerprint, InviteActionCompleted, now.Unix(),
		record.ID, InvitePending, InviteActionCreate, InviteActionProcessing)
	if err != nil {
		return fmt.Errorf("activate invite link: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrInviteState
	}
	record.Status = InviteActive
	if err := insertInviteAuditTx(ctx, tx, record, "invite_created", "created", now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit invite activation: %w", err)
	}
	return nil
}

func (s *Store) MarkInviteDelivered(ctx context.Context, publicID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin invite delivery completion: %w", err)
	}
	defer tx.Rollback()
	record, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelect+` WHERE public_id = ?`, publicID))
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE invite_links SET last_result = 'delivered', updated_at = ?
		WHERE id = ? AND status = ? AND action_kind = ? AND action_state = ? AND last_result = 'created'
	`, now.Unix(), record.ID, InviteActive, InviteActionCreate, InviteActionCompleted)
	if err != nil {
		return fmt.Errorf("complete invite delivery: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrInviteState
	}
	if err := insertInviteAuditTx(ctx, tx, record, "invite_delivered", "delivered", now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit invite delivery completion: %w", err)
	}
	return nil
}

func (s *Store) MarkInviteCreateIndeterminate(ctx context.Context, publicID, result string, now time.Time) error {
	return s.finishInviteCreate(ctx, publicID, InvitePending, InviteActionProcessing, InviteIndeterminate, result, now)
}

func (s *Store) MarkInviteCreateFailed(ctx context.Context, publicID, result string, now time.Time) error {
	return s.finishInviteCreate(ctx, publicID, InvitePending, InviteActionProcessing, InviteFailed, result, now)
}

func (s *Store) finishInviteCreate(ctx context.Context, publicID, currentStatus, currentActionState, status, result string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin invite create completion: %w", err)
	}
	defer tx.Rollback()
	record, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelect+` WHERE public_id = ?`, publicID))
	if err != nil {
		return err
	}
	update, err := tx.ExecContext(ctx, `
		UPDATE invite_links SET status = ?, action_state = ?, last_result = ?, updated_at = ?
		WHERE id = ? AND status = ? AND action_kind = ? AND action_state = ?
	`, status, InviteActionCompleted, cleanInviteResult(result), now.Unix(), record.ID,
		currentStatus, InviteActionCreate, currentActionState)
	if err != nil {
		return fmt.Errorf("complete invite creation: %w", err)
	}
	changed, _ := update.RowsAffected()
	if changed != 1 {
		return ErrInviteState
	}
	record.Status = status
	if err := insertInviteAuditTx(ctx, tx, record, "invite_create_completed", cleanInviteResult(result), now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit invite create completion: %w", err)
	}
	return nil
}

func (s *Store) SettleStaleInviteCreates(ctx context.Context, cutoff, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin stale invite create settlement: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, inviteLinkSelect+`
		WHERE status = ? AND action_kind = ? AND action_state IN (?, ?) AND updated_at <= ?
		ORDER BY id
	`, InvitePending, InviteActionCreate, InviteActionPending, InviteActionProcessing, cutoff.Unix())
	if err != nil {
		return fmt.Errorf("list stale invite creates: %w", err)
	}
	var records []InviteLink
	for rows.Next() {
		record, err := scanInviteLink(rows)
		if err != nil {
			rows.Close()
			return err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate stale invite creates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close stale invite create list: %w", err)
	}
	for _, record := range records {
		status, resultText := InviteFailed, "interrupted_before_create"
		if record.ActionState == InviteActionProcessing {
			status, resultText = InviteIndeterminate, "create_result_unknown"
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE invite_links SET status = ?, action_state = ?, last_result = ?, updated_at = ?
			WHERE id = ? AND status = ? AND action_kind = ? AND action_state = ? AND updated_at <= ?
		`, status, InviteActionCompleted, resultText, now.Unix(), record.ID, InvitePending,
			InviteActionCreate, record.ActionState, cutoff.Unix())
		if err != nil {
			return fmt.Errorf("settle stale invite create: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed == 1 {
			record.Status = status
			if err := insertInviteAuditTx(ctx, tx, record, "invite_create_recovered", resultText, now); err != nil {
				return err
			}
		}
	}
	rows, err = tx.QueryContext(ctx, inviteLinkSelect+`
		WHERE status = ? AND action_kind = ? AND action_state = ?
		  AND last_result = 'created' AND updated_at <= ?
		ORDER BY id
	`, InviteActive, InviteActionCreate, InviteActionCompleted, cutoff.Unix())
	if err != nil {
		return fmt.Errorf("list unconfirmed invite deliveries: %w", err)
	}
	records = records[:0]
	for rows.Next() {
		record, err := scanInviteLink(rows)
		if err != nil {
			rows.Close()
			return err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate unconfirmed invite deliveries: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close unconfirmed invite delivery list: %w", err)
	}
	for _, record := range records {
		result, err := tx.ExecContext(ctx, `
			UPDATE invite_links
			SET status = ?, revoke_operation_key = ?, revoked_by_user_id = creator_user_id,
			    action_kind = ?, action_state = ?, attempts = 0,
			    last_result = 'delivery_result_unknown', updated_at = ?
			WHERE id = ? AND status = ? AND action_kind = ? AND action_state = ?
			  AND last_result = 'created' AND updated_at <= ?
		`, InviteRevocationPending, "recovery:"+record.PublicID, InviteActionRevoke,
			InviteActionPending, now.Unix(), record.ID, InviteActive, InviteActionCreate,
			InviteActionCompleted, cutoff.Unix())
		if err != nil {
			return fmt.Errorf("recover unconfirmed invite delivery: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed == 1 {
			record.Status = InviteRevocationPending
			record.RevokedByUserID = record.CreatorUserID
			if err := insertInviteAuditTx(ctx, tx, record, "invite_delivery_recovered", "delivery_result_unknown", now); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit stale invite create settlement: %w", err)
	}
	return nil
}

func (s *Store) GetInviteLink(ctx context.Context, chatID int64, publicID string) (InviteLink, error) {
	return scanInviteLink(s.db.QueryRowContext(ctx, inviteLinkSelect+` WHERE chat_id = ? AND public_id = ?`, chatID, publicID))
}

func (s *Store) ListInviteLinks(ctx context.Context, chatID int64, limit int) ([]InviteSummary, error) {
	if chatID == 0 || limit <= 0 || limit > 100 {
		return nil, ErrInviteInput
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT public_id, chat_id, display_name, status, last_result, attempts,
		       created_at, updated_at, revoked_at
		FROM invite_links WHERE chat_id = ? ORDER BY id DESC LIMIT ?
	`, chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("list invite links: %w", err)
	}
	defer rows.Close()
	var summaries []InviteSummary
	for rows.Next() {
		var summary InviteSummary
		var createdAt, updatedAt int64
		var revokedAt sql.NullInt64
		if err := rows.Scan(&summary.PublicID, &summary.ChatID, &summary.DisplayName,
			&summary.Status, &summary.LastResult, &summary.Attempts,
			&createdAt, &updatedAt, &revokedAt); err != nil {
			return nil, fmt.Errorf("scan invite summary: %w", err)
		}
		summary.CreatedAt = time.Unix(createdAt, 0).UTC()
		summary.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		summary.RevokedAt = timePointer(revokedAt)
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invite summaries: %w", err)
	}
	return summaries, nil
}

func (s *Store) RequestInviteRevocation(ctx context.Context, chatID int64, publicID, operationKey string, actorUserID int64, now time.Time) (InviteLink, bool, error) {
	if chatID == 0 || publicID == "" || !validOperationKey(operationKey) || actorUserID <= 0 {
		return InviteLink{}, false, ErrInviteInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InviteLink{}, false, fmt.Errorf("begin invite revocation request: %w", err)
	}
	defer tx.Rollback()
	record, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelect+` WHERE chat_id = ? AND public_id = ?`, chatID, publicID))
	if err != nil {
		return InviteLink{}, false, err
	}
	changed := false
	switch record.Status {
	case InviteActive:
		changed = true
	case InviteRevocationPending:
		if record.ActionState == InviteActionFailed && record.RevokeOperationKey != operationKey {
			changed = true
		} else {
			if err := tx.Commit(); err != nil {
				return InviteLink{}, false, fmt.Errorf("commit duplicate invite revocation request: %w", err)
			}
			return record, false, nil
		}
	case InviteRevoked:
		if err := tx.Commit(); err != nil {
			return InviteLink{}, false, fmt.Errorf("commit revoked invite lookup: %w", err)
		}
		return record, false, nil
	default:
		return InviteLink{}, false, ErrInviteState
	}
	if changed {
		_, err = tx.ExecContext(ctx, `
			UPDATE invite_links
			SET status = ?, revoke_operation_key = ?, revoked_by_user_id = ?,
			    action_kind = ?, action_state = ?, attempts = 0,
			    last_result = 'revocation_requested', updated_at = ?
			WHERE id = ?
		`, InviteRevocationPending, operationKey, actorUserID, InviteActionRevoke,
			InviteActionPending, now.Unix(), record.ID)
		if err != nil {
			return InviteLink{}, false, fmt.Errorf("request invite revocation: %w", err)
		}
		record.Status = InviteRevocationPending
		record.RevokeOperationKey = operationKey
		record.RevokedByUserID = actorUserID
		record.ActionKind = InviteActionRevoke
		record.ActionState = InviteActionPending
		record.Attempts = 0
		if err := insertInviteAuditTx(ctx, tx, record, "invite_revocation_requested", "pending", now); err != nil {
			return InviteLink{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return InviteLink{}, false, fmt.Errorf("commit invite revocation request: %w", err)
	}
	return record, true, nil
}

func (s *Store) ClaimInviteRevocation(ctx context.Context, publicID string, retryBefore, now time.Time, maxAttempts int) (InviteLink, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InviteLink{}, fmt.Errorf("begin invite revocation claim: %w", err)
	}
	defer tx.Rollback()
	record, claimed, err := claimInviteRevocationTx(ctx, tx, publicID, retryBefore, now, maxAttempts)
	if err != nil {
		return InviteLink{}, err
	}
	if !claimed {
		return InviteLink{}, ErrInviteState
	}
	if err := tx.Commit(); err != nil {
		return InviteLink{}, fmt.Errorf("commit invite revocation claim: %w", err)
	}
	return record, nil
}

func (s *Store) ClaimInviteRevocations(ctx context.Context, retryBefore, now time.Time, maxAttempts, limit int) ([]InviteLink, error) {
	if maxAttempts <= 0 || limit <= 0 || limit > 100 {
		return nil, ErrInviteInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin invite revocation claims: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT public_id FROM invite_links
		WHERE status = ? AND action_kind = ? AND attempts < ? AND (
			action_state = ? OR (action_state IN (?, ?) AND updated_at <= ?)
		)
		ORDER BY id LIMIT ?
	`, InviteRevocationPending, InviteActionRevoke, maxAttempts, InviteActionPending,
		InviteActionFailed, InviteActionProcessing, retryBefore.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("list invite revocations: %w", err)
	}
	var publicIDs []string
	for rows.Next() {
		var publicID string
		if err := rows.Scan(&publicID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan invite revocation identifier: %w", err)
		}
		publicIDs = append(publicIDs, publicID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate invite revocation identifiers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close invite revocation identifiers: %w", err)
	}
	claimed := make([]InviteLink, 0, len(publicIDs))
	for _, publicID := range publicIDs {
		record, ok, err := claimInviteRevocationTx(ctx, tx, publicID, retryBefore, now, maxAttempts)
		if err != nil {
			return nil, err
		}
		if ok {
			claimed = append(claimed, record)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit invite revocation claims: %w", err)
	}
	return claimed, nil
}

func claimInviteRevocationTx(ctx context.Context, tx *sql.Tx, publicID string, retryBefore, now time.Time, maxAttempts int) (InviteLink, bool, error) {
	result, err := tx.ExecContext(ctx, `
		UPDATE invite_links
		SET action_state = ?, attempts = attempts + 1, updated_at = ?
		WHERE public_id = ? AND status = ? AND action_kind = ? AND attempts < ? AND (
			action_state = ? OR (action_state IN (?, ?) AND updated_at <= ?)
		)
	`, InviteActionProcessing, now.Unix(), publicID, InviteRevocationPending,
		InviteActionRevoke, maxAttempts, InviteActionPending, InviteActionFailed,
		InviteActionProcessing, retryBefore.Unix())
	if err != nil {
		return InviteLink{}, false, fmt.Errorf("claim invite revocation: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return InviteLink{}, false, nil
	}
	record, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelect+` WHERE public_id = ?`, publicID))
	return record, true, err
}

func (s *Store) CompleteInviteRevocation(ctx context.Context, id int64, result string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin invite revocation completion: %w", err)
	}
	defer tx.Rollback()
	record, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelect+` WHERE id = ?`, id))
	if err != nil {
		return err
	}
	update, err := tx.ExecContext(ctx, `
		UPDATE invite_links
		SET status = ?, encrypted_link = X'', link_nonce = X'', action_state = ?,
		    last_result = ?, revoked_at = ?, updated_at = ?
		WHERE id = ? AND status = ? AND action_kind = ? AND action_state = ?
	`, InviteRevoked, InviteActionCompleted, cleanInviteResult(result), now.Unix(), now.Unix(),
		id, InviteRevocationPending, InviteActionRevoke, InviteActionProcessing)
	if err != nil {
		return fmt.Errorf("complete invite revocation: %w", err)
	}
	changed, _ := update.RowsAffected()
	if changed != 1 {
		return ErrInviteState
	}
	record.Status = InviteRevoked
	if err := insertInviteAuditTx(ctx, tx, record, "invite_revoked", cleanInviteResult(result), now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit invite revocation completion: %w", err)
	}
	return nil
}

func (s *Store) FailInviteRevocation(ctx context.Context, id int64, result string, now time.Time) error {
	resultText := cleanInviteResult(result)
	update, err := s.db.ExecContext(ctx, `
		UPDATE invite_links SET action_state = ?, last_result = ?, updated_at = ?
		WHERE id = ? AND status = ? AND action_kind = ? AND action_state = ?
	`, InviteActionFailed, resultText, now.Unix(), id, InviteRevocationPending,
		InviteActionRevoke, InviteActionProcessing)
	if err != nil {
		return fmt.Errorf("fail invite revocation: %w", err)
	}
	changed, _ := update.RowsAffected()
	if changed != 1 {
		return ErrInviteState
	}
	return nil
}

func validNewInvite(record InviteLink) bool {
	return validInvitePublicID(record.PublicID) && validOperationKey(record.CreateOperationKey) &&
		record.ChatID != 0 && record.CreatorUserID > 0 && validStoredInviteName(record.DisplayName, 64) &&
		validStoredInviteName(record.TelegramName, 32) && record.RevokeOperationKey == "" &&
		len(record.EncryptedLink) == 0 && len(record.LinkNonce) == 0 && len(record.LinkFingerprint) == 0
}

func validInvitePublicID(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= '2' && character <= '7')) {
			return false
		}
	}
	return true
}

func validOperationKey(value string) bool {
	return value != "" && len(value) <= 128 && !strings.ContainsAny(value, "\r\n\x00")
}

func validStoredInviteName(value string, maximumRunes int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximumRunes &&
		!strings.ContainsAny(value, "\r\n\x00")
}

func cleanInviteResult(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return "invite_operation"
	}
	for _, character := range value {
		if !(character == '_' || character == '-' ||
			(character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')) {
			return "invite_operation"
		}
	}
	return value
}

func insertInviteAuditTx(ctx context.Context, tx *sql.Tx, record InviteLink, eventType, result string, now time.Time) error {
	metadata, _ := json.Marshal(map[string]any{
		"public_id": record.PublicID,
		"name":      record.DisplayName,
		"status":    record.Status,
		"result":    cleanInviteResult(result),
	})
	actor := record.CreatorUserID
	if record.RevokedByUserID > 0 {
		actor = record.RevokedByUserID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events (chat_id, actor_user_id, event_type, metadata_json, created_at)
		VALUES (?, ?, ?, json(?), ?)
	`, record.ChatID, actor, eventType, string(metadata), now.Unix()); err != nil {
		return fmt.Errorf("audit invite operation: %w", err)
	}
	return nil
}

const inviteLinkSelect = `
	SELECT id, public_id, create_operation_key, COALESCE(revoke_operation_key, ''),
	       chat_id, display_name, telegram_name, creator_user_id,
	       COALESCE(revoked_by_user_id, 0), status, encrypted_link, link_nonce,
	       link_fingerprint, action_kind, action_state, attempts, last_result,
	       revoked_at, created_at, updated_at
	FROM invite_links`

func scanInviteLink(row rowScanner) (InviteLink, error) {
	var record InviteLink
	var revokedAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&record.ID, &record.PublicID, &record.CreateOperationKey,
		&record.RevokeOperationKey, &record.ChatID, &record.DisplayName,
		&record.TelegramName, &record.CreatorUserID, &record.RevokedByUserID,
		&record.Status, &record.EncryptedLink, &record.LinkNonce,
		&record.LinkFingerprint, &record.ActionKind, &record.ActionState,
		&record.Attempts, &record.LastResult, &revokedAt, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return InviteLink{}, ErrInviteNotFound
		}
		return InviteLink{}, fmt.Errorf("read invite link record: %w", err)
	}
	record.RevokedAt = timePointer(revokedAt)
	record.CreatedAt = time.Unix(createdAt, 0).UTC()
	record.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return record, nil
}
