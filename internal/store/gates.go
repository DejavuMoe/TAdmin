package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Gate sources.
const (
	SourceJoin    = "join"
	SourceRequest = "request"
	SourceGuard   = "guard"
)

// Gate states.
const (
	StateNew       = "new"
	StateChallenge = "challenge"
	StateReview    = "review"
	StateResolving = "resolving"
	StateClosed    = "closed"
)

// Gate outcomes.
const (
	OutcomePass    = "pass"    // solved the challenge
	OutcomeApprove = "approve" // approved by an admin
	OutcomeFail    = "fail"    // attempts exhausted
	OutcomeTimeout = "timeout" // deadline passed
	OutcomeReject  = "reject"  // rejected by an admin
	OutcomeBan     = "ban"     // banned by an admin
	OutcomeLeft    = "left"    // user left / request withdrawn
	OutcomeCAS     = "cas"     // listed in CAS
	OutcomeCancel  = "cancel"  // gate abandoned (group removed, admin promoted...)
)

// Positive reports outcomes that admit the user.
func Positive(outcome string) bool { return outcome == OutcomePass || outcome == OutcomeApprove }

// Gate flags.
const (
	FlagLockdown  = 1 << iota // created during raid lockdown
	FlagHelp                  // user asked for help
	FlagEphemeral             // ephemeral captcha delivered
	FlagHighRisk              // AI risk high
	FlagPrivate               // user opened the private-chat challenge
	FlagWebApp                // user opened the Mini App
	FlagFallback              // created from a join service message (no invite-link details)
)

// Gate is one admission workflow.
type Gate struct {
	ID           int64
	ChatID       int64
	UserID       int64
	Source       string
	State        string
	Outcome      string
	Delivery     string
	Kind         string
	Seed         []byte
	Gen          int
	Picks        string
	Attempts     int
	MaxAttempts  int
	Deadline     time.Time
	NextAt       time.Time
	Steps        int
	Retries      int
	LastError    string
	Token        string
	UserName     string
	UserUsername string
	Lang         string
	UserChatID   int64
	QueryID      string
	PrevPerms    string
	PrevUntil    int64
	PromptMsg    int
	CaptchaChat  int64
	CaptchaMsg   int
	EphMsg       int
	ReviewChat   int64
	ReviewMsg    int
	JoinMsg      int
	Risk         float64
	RiskReason   string
	Flags        int
	ActorID      int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ClosedAt     time.Time
}

// Has reports whether flag is set.
func (g *Gate) Has(flag int) bool { return g.Flags&flag != 0 }

// Done reports whether a resolve step bit is set.
func (g *Gate) Done(step int) bool { return g.Steps&step != 0 }

// Open reports whether the gate is not closed.
func (g *Gate) Open() bool { return g.State != StateClosed }

// Answering reports whether the user can still answer.
func (g *Gate) Answering() bool { return g.State == StateChallenge }

const gateCols = `id, chat_id, user_id, source, state, outcome, delivery, kind, seed, gen, picks, attempts, max_attempts,
deadline, next_at, steps, retries, last_error, token, user_name, user_username, lang, user_chat_id, query_id,
prev_perms, prev_until, prompt_msg, captcha_chat, captcha_msg, eph_msg, review_chat, review_msg, join_msg,
risk, risk_reason, flags, actor_id, created_at, updated_at, closed_at`

func scanGate(row interface{ Scan(...any) error }) (*Gate, error) {
	var g Gate
	var deadline, next, created, updated, closed int64
	err := row.Scan(&g.ID, &g.ChatID, &g.UserID, &g.Source, &g.State, &g.Outcome, &g.Delivery, &g.Kind, &g.Seed, &g.Gen,
		&g.Picks, &g.Attempts, &g.MaxAttempts, &deadline, &next, &g.Steps, &g.Retries, &g.LastError, &g.Token,
		&g.UserName, &g.UserUsername, &g.Lang, &g.UserChatID, &g.QueryID, &g.PrevPerms, &g.PrevUntil, &g.PromptMsg,
		&g.CaptchaChat, &g.CaptchaMsg, &g.EphMsg, &g.ReviewChat, &g.ReviewMsg, &g.JoinMsg, &g.Risk, &g.RiskReason,
		&g.Flags, &g.ActorID, &created, &updated, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.Deadline, g.NextAt = fromUnix(deadline), fromUnix(next)
	g.CreatedAt, g.UpdatedAt, g.ClosedAt = fromUnix(created), fromUnix(updated), fromUnix(closed)
	return &g, nil
}

// CreateGate inserts g. If an open gate already exists for the same chat
// and user, it is returned with created=false.
func (s *Store) CreateGate(ctx context.Context, g *Gate) (created bool, existing *Gate, err error) {
	now := s.now()
	g.CreatedAt, g.UpdatedAt = now, now
	res, err := s.db.ExecContext(ctx, `INSERT INTO gates(chat_id, user_id, source, state, outcome, delivery, kind, seed, gen, picks,
attempts, max_attempts, deadline, next_at, steps, retries, last_error, token, user_name, user_username, lang, user_chat_id, query_id,
prev_perms, prev_until, prompt_msg, captcha_chat, captcha_msg, eph_msg, review_chat, review_msg, join_msg, risk, risk_reason,
flags, actor_id, created_at, updated_at, closed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`,
		g.ChatID, g.UserID, g.Source, g.State, g.Outcome, g.Delivery, g.Kind, g.Seed, g.Gen, g.Picks, g.Attempts, g.MaxAttempts,
		unixTime(g.Deadline), unixTime(g.NextAt), g.Steps, g.Retries, g.LastError, g.Token, g.UserName, g.UserUsername, g.Lang,
		g.UserChatID, g.QueryID, g.PrevPerms, g.PrevUntil, g.PromptMsg, g.CaptchaChat, g.CaptchaMsg, g.EphMsg, g.ReviewChat,
		g.ReviewMsg, g.JoinMsg, g.Risk, g.RiskReason, g.Flags, g.ActorID, now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			ex, gerr := s.OpenGateFor(ctx, g.ChatID, g.UserID)
			if gerr == nil {
				return false, ex, nil
			}
		}
		return false, nil, err
	}
	g.ID, _ = res.LastInsertId()
	return true, nil, nil
}

// SaveGate persists all mutable fields of g.
func (s *Store) SaveGate(ctx context.Context, g *Gate) error {
	g.UpdatedAt = s.now()
	if g.State == StateClosed && g.ClosedAt.IsZero() {
		g.ClosedAt = g.UpdatedAt
	}
	_, err := s.db.ExecContext(ctx, `UPDATE gates SET state=?, outcome=?, delivery=?, kind=?, gen=?, picks=?, attempts=?, max_attempts=?,
deadline=?, next_at=?, steps=?, retries=?, last_error=?, user_name=?, user_username=?, lang=?, user_chat_id=?, query_id=?,
prev_perms=?, prev_until=?, prompt_msg=?, captcha_chat=?, captcha_msg=?, eph_msg=?, review_chat=?, review_msg=?, join_msg=?,
risk=?, risk_reason=?, flags=?, actor_id=?, updated_at=?, closed_at=? WHERE id=?`,
		g.State, g.Outcome, g.Delivery, g.Kind, g.Gen, g.Picks, g.Attempts, g.MaxAttempts, unixTime(g.Deadline), unixTime(g.NextAt),
		g.Steps, g.Retries, g.LastError, g.UserName, g.UserUsername, g.Lang, g.UserChatID, g.QueryID, g.PrevPerms, g.PrevUntil,
		g.PromptMsg, g.CaptchaChat, g.CaptchaMsg, g.EphMsg, g.ReviewChat, g.ReviewMsg, g.JoinMsg, g.Risk, g.RiskReason,
		g.Flags, g.ActorID, g.UpdatedAt.Unix(), unixTime(g.ClosedAt), g.ID)
	return err
}

// GetGate loads a gate by id.
func (s *Store) GetGate(ctx context.Context, id int64) (*Gate, error) {
	return scanGate(s.db.QueryRowContext(ctx, `SELECT `+gateCols+` FROM gates WHERE id = ?`, id))
}

// GateByToken loads a gate by its token.
func (s *Store) GateByToken(ctx context.Context, token string) (*Gate, error) {
	return scanGate(s.db.QueryRowContext(ctx, `SELECT `+gateCols+` FROM gates WHERE token = ?`, token))
}

// OpenGateFor returns the open gate for (chat, user).
func (s *Store) OpenGateFor(ctx context.Context, chatID, userID int64) (*Gate, error) {
	return scanGate(s.db.QueryRowContext(ctx, `SELECT `+gateCols+` FROM gates WHERE chat_id = ? AND user_id = ? AND state <> 'closed'`, chatID, userID))
}

// LastClosedGate returns the most recently closed gate for (chat, user).
func (s *Store) LastClosedGate(ctx context.Context, chatID, userID int64) (*Gate, error) {
	return scanGate(s.db.QueryRowContext(ctx, `SELECT `+gateCols+` FROM gates WHERE chat_id = ? AND user_id = ? AND state = 'closed' ORDER BY closed_at DESC, id DESC LIMIT 1`, chatID, userID))
}

// OpenGatesForUser lists open gates of a user across chats (private-chat entry).
func (s *Store) OpenGatesForUser(ctx context.Context, userID int64) ([]*Gate, error) {
	return s.queryGates(ctx, `SELECT `+gateCols+` FROM gates WHERE user_id = ? AND state <> 'closed' ORDER BY id DESC LIMIT 20`, userID)
}

// OpenGates lists open gates of a chat, oldest first.
func (s *Store) OpenGates(ctx context.Context, chatID int64, limit int) ([]*Gate, error) {
	return s.queryGates(ctx, `SELECT `+gateCols+` FROM gates WHERE chat_id = ? AND state <> 'closed' ORDER BY id LIMIT ?`, chatID, limit)
}

// CountOpenGates counts open gates per chat.
func (s *Store) CountOpenGates(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT chat_id, COUNT(*) FROM gates WHERE state <> 'closed' GROUP BY chat_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// DueGates returns ids of open gates with next_at <= now, oldest first.
func (s *Store) DueGates(ctx context.Context, now time.Time, limit int) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM gates WHERE state <> 'closed' AND next_at <= ? ORDER BY next_at, id LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// NextDue returns the earliest next_at among open gates.
func (s *Store) NextDue(ctx context.Context) (time.Time, bool, error) {
	var v sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(next_at) FROM gates WHERE state <> 'closed'`).Scan(&v); err != nil {
		return time.Time{}, false, err
	}
	if !v.Valid {
		return time.Time{}, false, nil
	}
	return time.Unix(v.Int64, 0), true, nil
}

// RecentFailures counts failed/timed-out gates for (chat, user) closed after since.
func (s *Store) RecentFailures(ctx context.Context, chatID, userID int64, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gates WHERE chat_id = ? AND user_id = ? AND state = 'closed'
AND outcome IN ('fail','timeout') AND closed_at >= ?`, chatID, userID, since.Unix()).Scan(&n)
	return n, err
}

// RecentPass reports whether the user passed a request/guard gate in chat after since.
func (s *Store) RecentPass(ctx context.Context, chatID, userID int64, since time.Time) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gates WHERE chat_id = ? AND user_id = ? AND outcome IN ('pass','approve')
AND source IN ('request','guard') AND updated_at >= ?`, chatID, userID, since.Unix()).Scan(&n)
	return n > 0, err
}

func (s *Store) queryGates(ctx context.Context, q string, args ...any) ([]*Gate, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Gate
	for rows.Next() {
		g, err := scanGate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
