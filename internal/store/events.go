package store

import (
	"context"
	"database/sql"
	"time"
)

// Event kinds recorded for audit and statistics.
const (
	EvJoin       = "join"        // verification started
	EvPass       = "pass"        // solved (value = solve time ms)
	EvApprove    = "approve"     // admin approved
	EvFail       = "fail"        // attempts exhausted
	EvTimeout    = "timeout"     // deadline
	EvReject     = "reject"      // admin rejected
	EvBan        = "ban"         // admin banned
	EvLeft       = "left"        // left during verification
	EvCAS        = "cas"         // CAS listed
	EvBotKicked  = "bot_kicked"  // bot account added by non-admin removed
	EvExempt     = "exempt"      // skipped verification
	EvRaid       = "raid"        // lockdown started
	EvUnpin      = "unpin"       // channel pin removed (value = count)
	EvMask       = "mask"        // channel identity blocked
	EvAdminCmd   = "admin_cmd"   // admin command executed
	EvHelp       = "help"        // user asked for help
	EvActionFail = "action_fail" // Telegram action failed permanently
	EvRefresh    = "refresh"     // link preview refresh attempt (chat 0)
)

// Event is one audit row.
type Event struct {
	At      time.Time
	ChatID  int64
	Kind    string
	UserID  int64
	ActorID int64
	Value   int64
	Data    string
}

// AddEvent records an event.
func (s *Store) AddEvent(ctx context.Context, e Event) error {
	if e.At.IsZero() {
		e.At = s.now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO events(at, chat_id, kind, user_id, actor_id, value, data) VALUES (?,?,?,?,?,?,?)`,
		e.At.Unix(), e.ChatID, e.Kind, e.UserID, e.ActorID, e.Value, e.Data)
	return err
}

// CountEvents counts events of kind in chat since t.
func (s *Store) CountEvents(ctx context.Context, chatID int64, kind string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE chat_id = ? AND kind = ? AND at >= ?`, chatID, kind, since.Unix()).Scan(&n)
	return n, err
}

// DayStats aggregates one day.
type DayStats struct {
	Day     time.Time
	Counts  map[string]int
	SolveMS []int64
}

// Stats returns per-day counts for chatID (0 = all chats) over days days
// ending today in loc.
func (s *Store) Stats(ctx context.Context, chatID int64, days int, loc *time.Location) ([]DayStats, error) {
	now := s.now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	out := make([]DayStats, days)
	for i := range out {
		out[i] = DayStats{Day: start.AddDate(0, 0, i), Counts: map[string]int{}}
	}
	q := `SELECT at, kind, value FROM events WHERE at >= ?`
	args := []any{start.Unix()}
	if chatID != 0 {
		q += ` AND chat_id = ?`
		args = append(args, chatID)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var at, value int64
		var kind string
		if err := rows.Scan(&at, &kind, &value); err != nil {
			return nil, err
		}
		t := time.Unix(at, 0).In(loc)
		idx := int(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Sub(start).Hours() / 24)
		if idx < 0 || idx >= days {
			continue
		}
		out[idx].Counts[kind]++
		if kind == EvPass && value > 0 {
			out[idx].SolveMS = append(out[idx].SolveMS, value)
		}
	}
	return out, rows.Err()
}

// RecentEvents lists the latest events of a chat.
func (s *Store) RecentEvents(ctx context.Context, chatID int64, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, chat_id, kind, user_id, actor_id, value, data FROM events WHERE chat_id = ? ORDER BY id DESC LIMIT ?`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var at int64
		if err := rows.Scan(&at, &e.ChatID, &e.Kind, &e.UserID, &e.ActorID, &e.Value, &e.Data); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Deletion is a scheduled message deletion. UserID != 0 marks an ephemeral message.
type Deletion struct {
	ChatID    int64
	MessageID int
	UserID    int64
	Due       time.Time
}

// ScheduleDeletion schedules a message for deletion at due.
func (s *Store) ScheduleDeletion(ctx context.Context, d Deletion) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO deletions(chat_id, message_id, user_id, due) VALUES (?,?,?,?)
ON CONFLICT(chat_id, message_id, user_id) DO UPDATE SET due = excluded.due`, d.ChatID, d.MessageID, d.UserID, d.Due.Unix())
	return err
}

// DueDeletions pops up to limit due deletions.
func (s *Store) DueDeletions(ctx context.Context, now time.Time, limit int) ([]Deletion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT chat_id, message_id, user_id, due FROM deletions WHERE due <= ? ORDER BY due LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	var out []Deletion
	for rows.Next() {
		var d Deletion
		var due int64
		if err := rows.Scan(&d.ChatID, &d.MessageID, &d.UserID, &due); err != nil {
			rows.Close()
			return nil, err
		}
		d.Due = time.Unix(due, 0)
		out = append(out, d)
	}
	rows.Close()
	return out, rows.Err()
}

// DoneDeletion removes a scheduled deletion.
func (s *Store) DoneDeletion(ctx context.Context, d Deletion) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deletions WHERE chat_id = ? AND message_id = ? AND user_id = ?`, d.ChatID, d.MessageID, d.UserID)
	return err
}

// AddChannelPin records an automatic forward from the linked channel.
func (s *Store) AddChannelPin(ctx context.Context, chatID int64, messageID int, channelID int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO channel_pins(chat_id, message_id, channel_id, at) VALUES (?,?,?,?)`,
		chatID, messageID, channelID, s.now().Unix())
	return err
}

// ChannelPins lists recorded channel message ids, newest first.
func (s *Store) ChannelPins(ctx context.Context, chatID int64, limit int) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT message_id FROM channel_pins WHERE chat_id = ? ORDER BY message_id DESC LIMIT ?`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeleteChannelPins forgets recorded ids.
func (s *Store) DeleteChannelPins(ctx context.Context, chatID int64, ids []int) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM channel_pins WHERE chat_id = ? AND message_id = ?`, chatID, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Purge applies retention: closed gates after 30 days, events after 90,
// channel pins after 180 and preview urls not seen for 180 days.
func (s *Store) Purge(ctx context.Context) error {
	now := s.now()
	for _, q := range []struct {
		sql string
		age time.Duration
	}{
		{`DELETE FROM gates WHERE state = 'closed' AND closed_at < ?`, 30 * 24 * time.Hour},
		{`DELETE FROM events WHERE at < ?`, 90 * 24 * time.Hour},
		{`DELETE FROM channel_pins WHERE at < ?`, 180 * 24 * time.Hour},
		{`DELETE FROM preview_urls WHERE state <> 'queued' AND seen_at < ?`, 180 * 24 * time.Hour},
		{`DELETE FROM deletions WHERE due < ?`, 7 * 24 * time.Hour},
	} {
		if _, err := s.db.ExecContext(ctx, q.sql, now.Add(-q.age).Unix()); err != nil {
			return err
		}
	}
	return nil
}

// HasRecentEvent reports whether kind was recorded for (chat, user) after since.
func (s *Store) HasRecentEvent(ctx context.Context, chatID, userID int64, kind string, since time.Time) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE chat_id = ? AND user_id = ? AND kind = ? AND at >= ?`,
		chatID, userID, kind, since.Unix()).Scan(&n)
	return n > 0, err
}
