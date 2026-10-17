package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Preview url states.
const (
	URLIdle   = "idle"
	URLQueued = "queued"
	URLDone   = "done"
	URLFailed = "failed"
)

// PreviewSource is a sitemap or feed being watched.
type PreviewSource struct {
	ID           int64
	URL          string
	Kind         string
	Title        string
	Enabled      bool
	ETag         string
	LastModified string
	CheckedAt    time.Time
	NextCheck    time.Time
	LastError    string
	URLCount     int
	CreatedAt    time.Time
}

// PreviewURL is one tracked link.
type PreviewURL struct {
	URL         string
	SourceID    int64
	LastMod     time.Time
	SeenAt      time.Time
	State       string
	Priority    int
	QueuedAt    time.Time
	RefreshedAt time.Time
	Attempts    int
	Result      string
	Reply       string
}

const sourceCols = `id, url, kind, title, enabled, etag, last_modified, checked_at, next_check, last_error, url_count, created_at`

func scanSource(row interface{ Scan(...any) error }) (*PreviewSource, error) {
	var p PreviewSource
	var checked, next, created int64
	err := row.Scan(&p.ID, &p.URL, &p.Kind, &p.Title, &p.Enabled, &p.ETag, &p.LastModified, &checked, &next, &p.LastError, &p.URLCount, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.CheckedAt, p.NextCheck, p.CreatedAt = fromUnix(checked), fromUnix(next), fromUnix(created)
	return &p, nil
}

// AddSource inserts a source; an existing URL returns the existing row.
func (s *Store) AddSource(ctx context.Context, url, kind, title string) (*PreviewSource, bool, error) {
	if ex, err := scanSource(s.db.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM preview_sources WHERE url = ?`, url)); err == nil {
		return ex, false, nil
	}
	now := s.now().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO preview_sources(url, kind, title, enabled, created_at) VALUES (?,?,?,1,?)`, url, kind, title, now)
	if err != nil {
		return nil, false, err
	}
	id, _ := res.LastInsertId()
	src, err := s.GetSource(ctx, id)
	return src, true, err
}

// GetSource loads a source.
func (s *Store) GetSource(ctx context.Context, id int64) (*PreviewSource, error) {
	return scanSource(s.db.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM preview_sources WHERE id = ?`, id))
}

// ListSources lists all sources.
func (s *Store) ListSources(ctx context.Context) ([]*PreviewSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sourceCols+` FROM preview_sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PreviewSource
	for rows.Next() {
		p, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DueSources lists enabled sources whose next check is due.
func (s *Store) DueSources(ctx context.Context, now time.Time) ([]*PreviewSource, error) {
	all, err := s.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	var out []*PreviewSource
	for _, p := range all {
		if p.Enabled && !p.NextCheck.After(now) {
			out = append(out, p)
		}
	}
	return out, nil
}

// SaveSourceCheck stores the result of a check.
func (s *Store) SaveSourceCheck(ctx context.Context, p *PreviewSource) error {
	_, err := s.db.ExecContext(ctx, `UPDATE preview_sources SET etag=?, last_modified=?, checked_at=?, next_check=?, last_error=?, url_count=?, title=? WHERE id=?`,
		p.ETag, p.LastModified, unixTime(p.CheckedAt), unixTime(p.NextCheck), p.LastError, p.URLCount, p.Title, p.ID)
	return err
}

// SetSourceEnabled toggles a source.
func (s *Store) SetSourceEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE preview_sources SET enabled = ?, next_check = 0 WHERE id = ?`, enabled, id)
	return err
}

// TriggerSource makes a source due now.
func (s *Store) TriggerSource(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE preview_sources SET next_check = 0 WHERE id = ?`, id)
	return err
}

// DeleteSource removes a source and its non-queued urls.
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM preview_sources WHERE id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM preview_urls WHERE source_id = ? AND state <> 'queued'`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE preview_urls SET source_id = 0 WHERE source_id = ?`, id)
		return err
	})
}

// Observed is one url seen in a source.
type Observed struct {
	URL     string
	Updated time.Time
}

// ObserveResult summarises ObserveURLs.
type ObserveResult struct {
	New, Changed, Queued int
}

// ObserveURLs records entries of a source. New urls are queued when
// queueNew is set; known urls are queued when their lastmod advanced past
// the last refresh.
func (s *Store) ObserveURLs(ctx context.Context, sourceID int64, entries []Observed, queueNew bool) (ObserveResult, error) {
	var r ObserveResult
	now := s.now().Unix()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM preview_sources WHERE id = ?`, sourceID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		for _, e := range entries {
			var lastmod, refreshed int64
			var state string
			err := tx.QueryRowContext(ctx, `SELECT lastmod, refreshed_at, state FROM preview_urls WHERE url = ?`, e.URL).Scan(&lastmod, &refreshed, &state)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				r.New++
				st, queued := URLIdle, int64(0)
				if queueNew {
					st, queued = URLQueued, now
					r.Queued++
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO preview_urls(url, source_id, lastmod, seen_at, state, queued_at) VALUES (?,?,?,?,?,?)`,
					e.URL, sourceID, unixTime(e.Updated), now, st, queued); err != nil {
					return err
				}
			case err != nil:
				return err
			default:
				mod := unixTime(e.Updated)
				if mod > lastmod && lastmod != 0 && state != URLQueued && mod > refreshed {
					r.Changed++
					r.Queued++
					if _, err := tx.ExecContext(ctx, `UPDATE preview_urls SET lastmod=?, seen_at=?, state='queued', queued_at=?, attempts=0 WHERE url=?`, mod, now, now, e.URL); err != nil {
						return err
					}
				} else if _, err := tx.ExecContext(ctx, `UPDATE preview_urls SET lastmod=MAX(lastmod, ?), seen_at=? WHERE url=?`, mod, now, e.URL); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return r, err
}

// EnqueueURL queues a url with priority (manual requests use higher priority).
func (s *Store) EnqueueURL(ctx context.Context, url string, sourceID int64, priority int) error {
	now := s.now().Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO preview_urls(url, source_id, seen_at, state, priority, queued_at) VALUES (?,?,?,'queued',?,?)
ON CONFLICT(url) DO UPDATE SET state='queued', priority=MAX(preview_urls.priority, excluded.priority), queued_at=excluded.queued_at, attempts=0`,
		url, sourceID, now, priority, now)
	return err
}

// EnqueueSource queues all idle/failed urls of a source.
func (s *Store) EnqueueSource(ctx context.Context, sourceID int64) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE preview_urls SET state='queued', queued_at=?, attempts=0 WHERE source_id=? AND state <> 'queued'`, s.now().Unix(), sourceID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ClearQueue drops all queued urls back to idle.
func (s *Store) ClearQueue(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE preview_urls SET state='idle', priority=0 WHERE state='queued'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// NextQueued returns the next queued url whose queued_at <= now.
func (s *Store) NextQueued(ctx context.Context, now time.Time) (*PreviewURL, error) {
	var u PreviewURL
	var lastmod, seen, queued, refreshed int64
	err := s.db.QueryRowContext(ctx, `SELECT url, source_id, lastmod, seen_at, state, priority, queued_at, refreshed_at, attempts, result, reply
FROM preview_urls WHERE state='queued' AND queued_at <= ? ORDER BY priority DESC, queued_at, url LIMIT 1`, now.Unix()).
		Scan(&u.URL, &u.SourceID, &lastmod, &seen, &u.State, &u.Priority, &queued, &refreshed, &u.Attempts, &u.Result, &u.Reply)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.LastMod, u.SeenAt, u.QueuedAt, u.RefreshedAt = fromUnix(lastmod), fromUnix(seen), fromUnix(queued), fromUnix(refreshed)
	return &u, nil
}

// FinishURL records a refresh result.
func (s *Store) FinishURL(ctx context.Context, url, state, result, reply string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE preview_urls SET state=?, result=?, reply=?, refreshed_at=?, attempts=attempts+1, priority=0 WHERE url=?`,
		state, result, reply, s.now().Unix(), url)
	return err
}

// RetryURL requeues a url at a later time.
func (s *Store) RetryURL(ctx context.Context, url string, at time.Time, reply string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE preview_urls SET queued_at=?, attempts=attempts+1, reply=? WHERE url=?`, at.Unix(), reply, url)
	return err
}

// DeferURL postpones work without using an attempt for an untried refresh.
func (s *Store) DeferURL(ctx context.Context, url string, at time.Time, reply string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE preview_urls SET queued_at=?, reply=? WHERE url=?`, at.Unix(), reply, url)
	return err
}

// QueueStats summarises the refresh queue.
type QueueStats struct {
	Queued, DoneSince, FailedSince, Tracked int
}

// PreviewStats returns queue counters; *Since count refreshes after since.
func (s *Store) PreviewStats(ctx context.Context, since time.Time) (QueueStats, error) {
	var q QueueStats
	err := s.db.QueryRowContext(ctx, `SELECT
  COALESCE(SUM(state='queued'),0),
  COALESCE(SUM(state='done' AND refreshed_at >= ?),0),
  COALESCE(SUM(state='failed' AND refreshed_at >= ?),0),
  COUNT(*) FROM preview_urls`, since.Unix(), since.Unix()).Scan(&q.Queued, &q.DoneSince, &q.FailedSince, &q.Tracked)
	return q, err
}

// RecentRefreshes lists the most recent refresh results.
func (s *Store) RecentRefreshes(ctx context.Context, limit int) ([]PreviewURL, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT url, state, result, refreshed_at FROM preview_urls WHERE refreshed_at > 0 ORDER BY refreshed_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PreviewURL
	for rows.Next() {
		var u PreviewURL
		var at int64
		if err := rows.Scan(&u.URL, &u.State, &u.Result, &at); err != nil {
			return nil, err
		}
		u.RefreshedAt = fromUnix(at)
		out = append(out, u)
	}
	return out, rows.Err()
}
