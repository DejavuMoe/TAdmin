package store

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) ListManagedChats(ctx context.Context) ([]ManagedChat, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT chat_id, title, enabled, verification_timeout_seconds FROM managed_chats ORDER BY chat_id LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chats []ManagedChat
	for rows.Next() {
		var c ManagedChat
		if err := rows.Scan(&c.ChatID, &c.Title, &c.Enabled, &c.VerificationTimeoutSeconds); err != nil {
			return nil, err
		}
		chats = append(chats, c)
	}
	return chats, rows.Err()
}

func (s *Store) SetVerificationTimeout(ctx context.Context, chatID int64, seconds int, actorID int64, now time.Time) error {
	if seconds < 30 || seconds > 900 || actorID <= 0 {
		return ErrJoinCaseState
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE managed_chats SET verification_timeout_seconds = ?, updated_at = ? WHERE chat_id = ? AND enabled = 1", seconds, now.Unix(), chatID)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrManagedChatNotFound
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO audit_events(chat_id, actor_user_id, event_type, metadata_json, created_at) VALUES (?, ?, 'timeout_changed', json_object('seconds', ?), ?)", chatID, actorID, seconds, now.Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PollOffset(ctx context.Context) (int64, error) {
	var value int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM bot_state WHERE key = 'poll_offset'), 0)").Scan(&value)
	return value, err
}
func (s *Store) SavePollOffset(ctx context.Context, value int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO bot_state(key,value) VALUES ('poll_offset', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", value)
	return err
}

func (s *Store) PauseManagedChat(ctx context.Context, chatID int64, reason string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "UPDATE managed_chats SET enabled = 0, updated_at = ? WHERE chat_id = ?", now.Unix(), chatID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO audit_events(chat_id,event_type,metadata_json,created_at) SELECT chat_id,'chat_paused',json_object('reason',?),? FROM managed_chats WHERE chat_id=?", reason, now.Unix(), chatID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CheckIntegrity(ctx context.Context) error {
	var result string
	if err := s.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("SQLite quick_check did not pass")
	}
	rows, err := s.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("SQLite foreign_key_check did not pass")
	}
	return rows.Err()
}

func (s *Store) VerificationCounts(ctx context.Context) (active, failed, cards int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM join_cases WHERE status NOT IN ('resolved','cancelled')),
 (SELECT count(*) FROM join_cases WHERE status NOT IN ('resolved','cancelled') AND action_state='failed'),
 (SELECT count(*) FROM review_items WHERE card_state IN ('pending','failed','processing'))`).Scan(&active, &failed, &cards)
	return
}

// A Token rotation keeps the bot ID; a different bot must not replay this DB.
func (s *Store) BindBot(ctx context.Context, botID int64) error {
	if botID <= 0 {
		return ErrJoinCaseState
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO bot_state(key,value) VALUES ('bot_id',?) ON CONFLICT(key) DO NOTHING`, botID); err != nil {
		return err
	}
	var saved int64
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM bot_state WHERE key='bot_id'`).Scan(&saved); err != nil {
		return err
	}
	if saved != botID {
		return fmt.Errorf("database belongs to a different Telegram bot")
	}
	return nil
}
