package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Chat statuses.
const (
	ChatPending = "pending" // added by someone other than the owner; awaiting approval
	ChatActive  = "active"
	ChatPaused  = "paused" // owner paused new verification
	ChatLeft    = "left"   // bot removed
)

// Delivery modes.
const (
	DeliveryOverlay = "overlay"
	DeliveryPublic  = "public"
	DeliveryPrivate = "private"
)

// Settings are per-group options edited from the control panel. Decoding
// always starts from DefaultSettings so new fields get sane defaults.
type Settings struct {
	Language string  `json:"lang"`
	Verify   Verify  `json:"verify"`
	Welcome  Welcome `json:"welcome"`
	Channel  Channel `json:"channel"`
	Clean    Clean   `json:"clean"`
	Raid     Raid    `json:"raid"`
	KickBots bool    `json:"kick_bots"`
}

// Verify configures admission verification.
type Verify struct {
	Enabled   bool   `json:"enabled"`
	Delivery  string `json:"delivery"`
	Kind      string `json:"kind"`
	Timeout   int    `json:"timeout"`
	Attempts  int    `json:"attempts"`
	OnFail    string `json:"on_fail"` // kick | review
	Cooldown  bool   `json:"cooldown"`
	Requests  bool   `json:"requests"`
	WebApp    bool   `json:"webapp"`
	CAS       bool   `json:"cas"`
	AI        string `json:"ai"` // off | hint | review
	ReviewTTL int    `json:"review_ttl"`
}

// Welcome configures the welcome message after passing.
type Welcome struct {
	Enabled     bool   `json:"enabled"`
	Text        string `json:"text"`
	Buttons     string `json:"buttons"`
	Ephemeral   bool   `json:"ephemeral"`
	DeleteAfter int    `json:"delete_after"`
}

// Channel configures linked-channel behaviour.
type Channel struct {
	Unpin      string `json:"unpin"` // off | unpin | keep_latest
	BlockMasks bool   `json:"block_masks"`
}

// Clean configures service message cleanup.
type Clean struct {
	Join     bool `json:"join"`
	Leave    bool `json:"leave"`
	Pin      bool `json:"pin"`
	Other    bool `json:"other"`
	Commands bool `json:"commands"`
}

// Raid configures join-surge protection.
type Raid struct {
	Threshold int `json:"threshold"` // joins per minute; 0 disables
	Minutes   int `json:"minutes"`
}

// DefaultSettings returns the defaults for newly managed groups.
func DefaultSettings() Settings {
	return Settings{
		Verify: Verify{
			Enabled: true, Delivery: DeliveryOverlay, Kind: "image", Timeout: 180, Attempts: 3,
			OnFail: "kick", Cooldown: true, Requests: true, WebApp: true, AI: "off", ReviewTTL: 900,
		},
		Welcome:  Welcome{Enabled: false, Ephemeral: true, DeleteAfter: 300},
		Channel:  Channel{Unpin: "off"},
		Clean:    Clean{Join: true, Leave: true, Pin: false, Other: false, Commands: true},
		Raid:     Raid{Threshold: 10, Minutes: 15},
		KickBots: true,
	}
}

// Chat is a managed group.
type Chat struct {
	ID            int64
	Title         string
	Username      string
	Status        string
	LinkedChatID  int64
	AddedBy       int64
	Settings      Settings
	LockdownUntil time.Time
	Problems      string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// InLockdown reports whether raid protection is active.
func (c *Chat) InLockdown(now time.Time) bool { return now.Before(c.LockdownUntil) }

// Lang returns the chat language or fallback.
func (c *Chat) Lang(fallback string) string {
	if c.Settings.Language != "" {
		return c.Settings.Language
	}
	return fallback
}

const chatCols = `chat_id, title, username, status, linked_chat_id, added_by, settings, lockdown_until, problems, created_at, updated_at`

func scanChat(row interface{ Scan(...any) error }) (*Chat, error) {
	var c Chat
	var settings string
	var lockdown, created, updated int64
	if err := row.Scan(&c.ID, &c.Title, &c.Username, &c.Status, &c.LinkedChatID, &c.AddedBy, &settings, &lockdown, &c.Problems, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c.Settings = DefaultSettings()
	_ = json.Unmarshal([]byte(settings), &c.Settings)
	c.LockdownUntil, c.CreatedAt, c.UpdatedAt = fromUnix(lockdown), fromUnix(created), fromUnix(updated)
	return &c, nil
}

// GetChat loads a chat.
func (s *Store) GetChat(ctx context.Context, id int64) (*Chat, error) {
	return scanChat(s.db.QueryRowContext(ctx, `SELECT `+chatCols+` FROM chats WHERE chat_id = ?`, id))
}

// ListChats returns all chats that are not left, ordered by title.
func (s *Store) ListChats(ctx context.Context, includeLeft bool) ([]*Chat, error) {
	q := `SELECT ` + chatCols + ` FROM chats`
	if !includeLeft {
		q += ` WHERE status <> 'left'`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY title COLLATE NOCASE, chat_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertChat inserts or updates identity fields and status of a chat,
// keeping existing settings. New chats get DefaultSettings with lang.
func (s *Store) UpsertChat(ctx context.Context, c *Chat) error {
	now := s.now().Unix()
	settings := c.Settings
	if settings.Verify.Delivery == "" {
		settings = DefaultSettings()
		settings.Language = c.Settings.Language
	}
	data, _ := json.Marshal(settings)
	_, err := s.db.ExecContext(ctx, `INSERT INTO chats(`+chatCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, 0, '', ?, ?)
ON CONFLICT(chat_id) DO UPDATE SET title = excluded.title, username = excluded.username, status = excluded.status,
  linked_chat_id = excluded.linked_chat_id, added_by = CASE WHEN excluded.added_by <> 0 THEN excluded.added_by ELSE chats.added_by END,
  updated_at = excluded.updated_at`,
		c.ID, c.Title, c.Username, c.Status, c.LinkedChatID, c.AddedBy, string(data), now, now)
	return err
}

// SetChatStatus updates status.
func (s *Store) SetChatStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET status = ?, updated_at = ? WHERE chat_id = ?`, status, s.now().Unix(), id)
	return err
}

// SetChatProblems records the latest permission check summary.
func (s *Store) SetChatProblems(ctx context.Context, id int64, problems string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET problems = ?, updated_at = ? WHERE chat_id = ?`, problems, s.now().Unix(), id)
	return err
}

// SetChatInfo updates the title / username / linked channel.
func (s *Store) SetChatInfo(ctx context.Context, id int64, title, username string, linked int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET title = ?, username = ?, linked_chat_id = ?, updated_at = ? WHERE chat_id = ?`,
		title, username, linked, s.now().Unix(), id)
	return err
}

// UpdateSettings applies fn to the chat settings atomically.
func (s *Store) UpdateSettings(ctx context.Context, id int64, fn func(*Settings)) (*Chat, error) {
	var out *Chat
	err := s.tx(ctx, func(tx *sql.Tx) error {
		c, err := scanChat(tx.QueryRowContext(ctx, `SELECT `+chatCols+` FROM chats WHERE chat_id = ?`, id))
		if err != nil {
			return err
		}
		fn(&c.Settings)
		data, _ := json.Marshal(c.Settings)
		if _, err := tx.ExecContext(ctx, `UPDATE chats SET settings = ?, updated_at = ? WHERE chat_id = ?`, string(data), s.now().Unix(), id); err != nil {
			return err
		}
		out = c
		return nil
	})
	return out, err
}

// SetLockdown sets or clears raid lockdown.
func (s *Store) SetLockdown(ctx context.Context, id int64, until time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET lockdown_until = ?, updated_at = ? WHERE chat_id = ?`, unixTime(until), s.now().Unix(), id)
	return err
}

// MigrateChat moves a chat to a new id (group upgraded to supergroup).
func (s *Store) MigrateChat(ctx context.Context, from, to int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`UPDATE OR REPLACE chats SET chat_id = ? WHERE chat_id = ?`,
			`UPDATE events SET chat_id = ? WHERE chat_id = ?`,
			`UPDATE OR IGNORE gates SET chat_id = ? WHERE chat_id = ?`,
			`UPDATE OR IGNORE deletions SET chat_id = ? WHERE chat_id = ?`,
			`UPDATE OR IGNORE channel_pins SET chat_id = ? WHERE chat_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, to, from); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteChat forgets a chat and its history.
func (s *Store) DeleteChat(ctx context.Context, id int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM chats WHERE chat_id = ?`, `DELETE FROM gates WHERE chat_id = ?`,
			`DELETE FROM events WHERE chat_id = ?`, `DELETE FROM channel_pins WHERE chat_id = ?`,
			`DELETE FROM deletions WHERE chat_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
}
