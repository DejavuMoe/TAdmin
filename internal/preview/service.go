// Package preview keeps Telegram link previews fresh: it watches sitemaps
// and feeds for new or changed pages and refreshes them one by one through
// @WebpageBot using the optional MTProto userbot, slowly and politely.
package preview

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"tadmin/internal/preview/feeds"
	"tadmin/internal/store"
	"tadmin/internal/userbot"
)

// Refresher performs one preview refresh (implemented by *userbot.Manager).
type Refresher interface {
	Ready() bool
	RefreshPreview(ctx context.Context, url string) (userbot.RefreshResult, error)
}

// Config wires the service.
type Config struct {
	Store         *store.Store
	Fetcher       *feeds.Fetcher
	Refresher     Refresher // nil when no userbot is configured
	CheckInterval time.Duration
	MinGap        time.Duration
	DailyLimit    int
	Location      *time.Location
	Notify        func(ctx context.Context, text string)
	Lang          string
	Logger        *slog.Logger
	Now           func() time.Time
}

// Service is the preview refresher.
type Service struct {
	cfg  Config
	st   *store.Store
	log  *slog.Logger
	now  func() time.Time
	wake chan struct{}
	lane sync.Mutex // one refresh at a time

	mu          sync.Mutex
	floodUntil  time.Time
	lastRefresh time.Time
	nextAt      time.Time
	batch       batch
	checking    map[int64]bool
}

type batch struct {
	updated, noPreview, unknown, failed int
	started                             time.Time
}

func (b batch) total() int { return b.updated + b.noPreview + b.unknown + b.failed }

// New creates the service.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	return &Service{cfg: cfg, st: cfg.Store, log: cfg.Logger, now: cfg.Now, wake: make(chan struct{}, 1), checking: map[int64]bool{}}
}

// Enabled reports whether refreshing is possible at all (userbot configured).
func (s *Service) Enabled() bool { return s.cfg.Refresher != nil }

// Wake nudges the queue worker.
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run drives source checks and the refresh queue.
func (s *Service) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.checkLoop(ctx) }()
	go func() { defer wg.Done(); s.queueLoop(ctx) }()
	wg.Wait()
	return nil
}

func sleep(ctx context.Context, d time.Duration, wake <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
	case <-wake:
	}
	return true
}

func (s *Service) checkLoop(ctx context.Context) {
	if !sleep(ctx, 20*time.Second, nil) {
		return
	}
	for {
		srcs, err := s.st.DueSources(ctx, s.now())
		if err != nil && ctx.Err() == nil {
			s.log.Error("load due sources", "error", err)
		}
		for _, src := range srcs {
			if ctx.Err() != nil {
				return
			}
			if _, err := s.Check(ctx, src); err != nil {
				s.log.Warn("preview source check failed", "source", src.URL, "error", err)
			}
		}
		if !sleep(ctx, time.Minute, nil) {
			return
		}
	}
}

// Check fetches a source and records new/changed urls.
func (s *Service) Check(ctx context.Context, src *store.PreviewSource) (store.ObserveResult, error) {
	s.mu.Lock()
	if s.checking[src.ID] {
		s.mu.Unlock()
		return store.ObserveResult{}, nil
	}
	s.checking[src.ID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.checking, src.ID)
		s.mu.Unlock()
	}()
	entries, doc, err := s.collect(ctx, src)
	src.CheckedAt = s.now()
	src.NextCheck = src.CheckedAt.Add(s.cfg.CheckInterval)
	if err != nil {
		src.LastError = truncate(err.Error(), 300)
		// Back off faster on errors but never hammer the site.
		src.NextCheck = src.CheckedAt.Add(max(s.cfg.CheckInterval/4, 15*time.Minute))
		_ = s.st.SaveSourceCheck(ctx, src)
		return store.ObserveResult{}, err
	}
	src.LastError = ""
	if doc != nil {
		src.ETag, src.LastModified = doc.ETag, doc.LastModified
		if doc.Title != "" && src.Title == "" {
			src.Title = truncate(doc.Title, 100)
		}
	}
	var r store.ObserveResult
	if entries != nil {
		r, err = s.st.ObserveURLs(ctx, src.ID, entries, true)
		if err != nil {
			return r, err
		}
		src.URLCount = len(entries)
	}
	if err := s.st.SaveSourceCheck(ctx, src); err != nil {
		return r, err
	}
	if r.Queued > 0 {
		s.log.Info("preview urls queued", "source", src.URL, "new", r.New, "changed", r.Changed)
		s.Wake()
	}
	return r, nil
}

// collect returns the entries of a source; nil entries mean "not modified".
func (s *Service) collect(ctx context.Context, src *store.PreviewSource) ([]store.Observed, *feeds.Document, error) {
	doc, err := s.cfg.Fetcher.Fetch(ctx, src.URL, feeds.Conditional{ETag: src.ETag, LastModified: src.LastModified})
	if err != nil {
		return nil, nil, err
	}
	if doc.NotModified {
		return nil, doc, nil
	}
	var entries []feeds.Entry
	if doc.Kind == feeds.KindSitemapIndex {
		w, err := s.cfg.Fetcher.Walk(ctx, src.URL, feeds.DefaultMaxWalkURLs)
		if err != nil {
			return nil, nil, err
		}
		entries = w.Entries
	} else {
		entries = doc.Entries
	}
	out := make([]store.Observed, 0, len(entries))
	for _, e := range entries {
		out = append(out, store.Observed{URL: e.URL, Updated: e.Updated})
	}
	return out, doc, nil
}

// Discover lists candidate sources for any URL.
func (s *Service) Discover(ctx context.Context, rawURL string) ([]feeds.Candidate, error) {
	return s.cfg.Fetcher.Discover(ctx, rawURL)
}

// Add registers a source and records its current urls. With queueExisting
// all current urls are queued once; otherwise only future changes are.
func (s *Service) Add(ctx context.Context, c feeds.Candidate, queueExisting bool) (*store.PreviewSource, store.ObserveResult, error) {
	src, created, err := s.st.AddSource(ctx, c.URL, string(c.Kind), truncate(c.Title, 100))
	if err != nil {
		return nil, store.ObserveResult{}, err
	}
	if !created {
		return src, store.ObserveResult{}, nil
	}
	entries, doc, err := s.collect(ctx, src)
	src.CheckedAt = s.now()
	src.NextCheck = src.CheckedAt.Add(s.cfg.CheckInterval)
	if err != nil {
		src.LastError = truncate(err.Error(), 300)
		_ = s.st.SaveSourceCheck(ctx, src)
		return src, store.ObserveResult{}, err
	}
	if doc != nil {
		src.ETag, src.LastModified = doc.ETag, doc.LastModified
	}
	r, err := s.st.ObserveURLs(ctx, src.ID, entries, queueExisting)
	if err != nil {
		return src, r, err
	}
	src.URLCount = len(entries)
	_ = s.st.SaveSourceCheck(ctx, src)
	if r.Queued > 0 {
		s.Wake()
	}
	return src, r, nil
}

// Enqueue queues one url; manual requests jump the queue.
func (s *Service) Enqueue(ctx context.Context, rawURL string, manual bool) (string, error) {
	u, err := feeds.NormalizeURL(rawURL)
	if err != nil {
		return "", err
	}
	prio := 0
	if manual {
		prio = 10
	}
	if err := s.st.EnqueueURL(ctx, u, 0, prio); err != nil {
		return "", err
	}
	s.Wake()
	return u, nil
}

// RefreshNow refreshes one url immediately (manual action), waiting for the
// single refresh lane.
func (s *Service) RefreshNow(ctx context.Context, rawURL string) (userbot.RefreshResult, error) {
	if s.cfg.Refresher == nil {
		return userbot.RefreshResult{}, userbot.ErrNotReady
	}
	u, err := feeds.NormalizeURL(rawURL)
	if err != nil {
		return userbot.RefreshResult{}, err
	}
	s.lane.Lock()
	defer s.lane.Unlock()
	r, err := s.cfg.Refresher.RefreshPreview(ctx, u)
	s.markRefresh(ctx, err)
	s.noteFlood(err)
	if err == nil {
		state := store.URLDone
		if r.Outcome == userbot.OutcomeNoPreview {
			state = store.URLFailed
		}
		_ = s.st.EnqueueURL(ctx, u, 0, 0)
		_ = s.st.FinishURL(ctx, u, state, string(r.Outcome), r.Reply)
	}
	return r, err
}

func (s *Service) noteFlood(err error) {
	var fw *userbot.FloodWaitError
	if errors.As(err, &fw) {
		s.mu.Lock()
		s.floodUntil = s.now().Add(fw.Wait + 5*time.Second)
		s.mu.Unlock()
	}
}

func (s *Service) dayStart() time.Time {
	n := s.now().In(s.cfg.Location)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, s.cfg.Location)
}

func (s *Service) queueLoop(ctx context.Context) {
	for ctx.Err() == nil {
		wait := s.tick(ctx)
		s.mu.Lock()
		s.nextAt = s.now().Add(wait)
		s.mu.Unlock()
		if !sleep(ctx, wait, s.wake) {
			return
		}
	}
}

// tick processes at most one queued url and returns how long to wait.
func (s *Service) tick(ctx context.Context) time.Duration {
	if s.cfg.Refresher == nil || !s.cfg.Refresher.Ready() {
		return time.Minute
	}
	s.mu.Lock()
	flood := s.floodUntil.Sub(s.now())
	s.mu.Unlock()
	if flood > 0 {
		return flood
	}
	if gap := s.lastRefreshAt().Add(s.cfg.MinGap).Sub(s.now()); gap > 0 {
		return gap // a wake-up must never shorten the polite gap
	}
	done, err := s.st.CountEvents(ctx, 0, store.EvRefresh, s.dayStart())
	if err != nil {
		return time.Minute
	}
	if done >= s.cfg.DailyLimit {
		s.flushDigest(ctx, true)
		return max(time.Minute, s.dayStart().Add(24*time.Hour).Sub(s.now()))
	}
	u, err := s.st.NextQueued(ctx, s.now())
	if errors.Is(err, store.ErrNotFound) {
		s.flushDigest(ctx, false)
		return 10 * time.Minute
	}
	if err != nil {
		return time.Minute
	}
	if u.Priority == 0 && !u.RefreshedAt.IsZero() && s.now().Sub(u.RefreshedAt) < 24*time.Hour {
		// Refreshed within a day: postpone rather than hammer .
		_ = s.st.RetryURL(ctx, u.URL, u.RefreshedAt.Add(24*time.Hour), u.Reply)
		return time.Second
	}
	s.lane.Lock()
	rctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	r, err := s.cfg.Refresher.RefreshPreview(rctx, u.URL)
	cancel()
	s.markRefresh(ctx, err)
	s.lane.Unlock()
	s.noteFlood(err)
	var fw *userbot.FloodWaitError
	switch {
	case errors.As(err, &fw):
		_ = s.st.RetryURL(ctx, u.URL, s.now().Add(fw.Wait), "flood wait")
		s.log.Warn("preview refresh flood wait", "wait", fw.Wait)
		return fw.Wait + 5*time.Second
	case errors.Is(err, userbot.ErrNotReady):
		return time.Minute
	case err != nil && !errors.Is(err, userbot.ErrTimeout):
		s.recordBatch(func(b *batch) { b.failed++ })
		if u.Attempts+1 >= 3 {
			_ = s.st.FinishURL(ctx, u.URL, store.URLFailed, "error", truncate(err.Error(), 300))
		} else {
			_ = s.st.RetryURL(ctx, u.URL, s.now().Add(10*time.Minute), truncate(err.Error(), 300))
		}
	case err != nil: // timeout: the bot did not answer in time
		if u.Attempts+1 >= 3 {
			s.recordBatch(func(b *batch) { b.unknown++ })
			_ = s.st.FinishURL(ctx, u.URL, store.URLDone, string(userbot.OutcomeUnknown), "")
		} else {
			_ = s.st.RetryURL(ctx, u.URL, s.now().Add(5*time.Minute), "timeout")
		}
	default:
		state := store.URLDone
		switch r.Outcome {
		case userbot.OutcomeUpdated:
			s.recordBatch(func(b *batch) { b.updated++ })
		case userbot.OutcomeNoPreview:
			state = store.URLFailed
			s.recordBatch(func(b *batch) { b.noPreview++ })
		default:
			s.recordBatch(func(b *batch) { b.unknown++ })
		}
		_ = s.st.FinishURL(ctx, u.URL, state, string(r.Outcome), r.Reply)
	}
	gap := s.cfg.MinGap
	return gap + time.Duration(rand.Int64N(int64(gap/2)+1))
}

func (s *Service) recordBatch(fn func(*batch)) {
	s.mu.Lock()
	if s.batch.total() == 0 {
		s.batch.started = s.now()
	}
	fn(&s.batch)
	s.mu.Unlock()
}

// flushDigest sends one summary per finished batch.
func (s *Service) flushDigest(ctx context.Context, limited bool) {
	s.mu.Lock()
	b := s.batch
	s.batch = batch{}
	s.mu.Unlock()
	if b.total() == 0 || s.cfg.Notify == nil {
		return
	}
	var msg string
	if s.cfg.Lang == "en" {
		msg = fmt.Sprintf("🔄 <b>Preview refresh finished</b>\n✅ updated %d · 🚫 no preview %d · ❔ unknown %d · ⚠️ failed %d", b.updated, b.noPreview, b.unknown, b.failed)
		if limited {
			msg += "\n⏸ Daily limit reached; the rest continues tomorrow."
		}
	} else {
		msg = fmt.Sprintf("🔄 <b>预览刷新完成</b>\n✅ 已更新 %d · 🚫 无预览 %d · ❔ 未知 %d · ⚠️ 失败 %d", b.updated, b.noPreview, b.unknown, b.failed)
		if limited {
			msg += "\n⏸ 已达每日上限，剩余链接明天继续。"
		}
	}
	s.cfg.Notify(ctx, msg)
}

// Status summarises the refresher.
type Status struct {
	Enabled    bool
	Ready      bool
	Queue      store.QueueStats
	Sources    int
	DailyLimit int
	FloodUntil time.Time
	NextAt     time.Time
}

// Status returns the current status.
func (s *Service) Status(ctx context.Context) Status {
	st := Status{Enabled: s.Enabled(), DailyLimit: s.cfg.DailyLimit}
	if s.cfg.Refresher != nil {
		st.Ready = s.cfg.Refresher.Ready()
	}
	st.Queue, _ = s.st.PreviewStats(ctx, s.dayStart())
	st.Queue.DoneSince, _ = s.st.CountEvents(ctx, 0, store.EvRefresh, s.dayStart())
	if srcs, err := s.st.ListSources(ctx); err == nil {
		st.Sources = len(srcs)
	}
	s.mu.Lock()
	st.FloodUntil, st.NextAt = s.floodUntil, s.nextAt
	s.mu.Unlock()
	return st
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (s *Service) lastRefreshAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRefresh
}

// markRefresh records an attempt that reached Telegram, for the minimum gap
// and the daily limit (counted in an append-only log so re-queued urls
// cannot reset it).
func (s *Service) markRefresh(ctx context.Context, err error) {
	var fw *userbot.FloodWaitError
	if errors.Is(err, userbot.ErrNotReady) || errors.As(err, &fw) {
		return
	}
	s.mu.Lock()
	s.lastRefresh = s.now()
	s.mu.Unlock()
	_ = s.st.AddEvent(ctx, store.Event{ChatID: 0, Kind: store.EvRefresh})
}
