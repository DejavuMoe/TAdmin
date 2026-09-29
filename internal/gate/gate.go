// Package gate implements TAdmin's admission verification engine: a
// persistent per-member state machine driven by Telegram updates, button
// presses, Mini App requests and a due-time scheduler.
package gate

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"hash/fnv"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"tadmin/internal/intel"
	"tadmin/internal/roles"
	"tadmin/internal/store"
	"tadmin/internal/tg"
)

// Config wires the engine.
type Config struct {
	Store       *store.Store
	TG          *tg.Client
	Roles       *roles.Cache
	Logger      *slog.Logger
	OwnerID     int64
	BotID       int64
	BotUsername string
	Lang        string // default language
	WebURL      string // public Mini App origin, "" when disabled
	Assessor    intel.Assessor
	CAS         CASChecker
	Workers     int
	Now         func() time.Time
}

// Engine is the admission engine.
type Engine struct {
	cfg   Config
	st    *store.Store
	tg    *tg.Client
	log   *slog.Logger
	now   func() time.Time
	locks [64]sync.Mutex
	wake  chan struct{}

	mu       sync.Mutex
	inflight map[int64]bool
	alerts   map[string]time.Time
	notices  map[int64]time.Time // last raid notice per chat
	sem      chan struct{}
}

// New creates an engine.
func New(cfg Config) *Engine {
	if cfg.Workers <= 0 {
		cfg.Workers = 8
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Engine{
		cfg: cfg, st: cfg.Store, tg: cfg.TG, log: cfg.Logger, now: cfg.Now,
		wake:     make(chan struct{}, 1),
		inflight: map[int64]bool{},
		alerts:   map[string]time.Time{},
		notices:  map[int64]time.Time{},
		sem:      make(chan struct{}, cfg.Workers),
	}
}

func (e *Engine) lock(id int64) func() {
	h := fnv.New32a()
	h.Write([]byte(strconv.FormatInt(id, 10)))
	m := &e.locks[h.Sum32()%uint32(len(e.locks))]
	m.Lock()
	return m.Unlock
}

// Wake nudges the scheduler.
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Run drives due gates and scheduled deletions until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	// Message deletions are best-effort housekeeping; keep them off the
	// scheduler loop so rate limits never delay admissions.
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				e.runDeletions(ctx, e.now())
			}
		}
	}()
	timer := time.NewTimer(0)
	defer timer.Stop()
	lastPurge := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-e.wake:
		case <-timer.C:
		}
		now := e.now()
		ids, err := e.st.DueGates(ctx, now, 100)
		if err != nil && ctx.Err() == nil {
			e.log.Error("load due gates", "error", err)
		}
		for _, id := range ids {
			e.mu.Lock()
			busy := e.inflight[id]
			if !busy {
				e.inflight[id] = true
			}
			e.mu.Unlock()
			if busy {
				continue
			}
			select {
			case e.sem <- struct{}{}:
			case <-ctx.Done():
				return nil
			}
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				defer func() { <-e.sem }()
				defer func() {
					e.mu.Lock()
					delete(e.inflight, id)
					e.mu.Unlock()
				}()
				e.Process(ctx, id)
			}(id)
		}
		if now.Sub(lastPurge) > time.Hour {
			lastPurge = now
			if err := e.st.Purge(ctx); err != nil && ctx.Err() == nil {
				e.log.Warn("retention purge", "error", err)
			}
		}
		// Sleep until the next due gate, but never longer than 5s so that
		// scheduled deletions and in-flight retries are picked up promptly.
		next := 5 * time.Second
		if len(ids) == 100 {
			next = 200 * time.Millisecond
		} else if at, ok, err := e.st.NextDue(ctx); err == nil && ok {
			if d := at.Sub(e.now()); d < next {
				next = max(d, 200*time.Millisecond)
			}
		}
		timer.Reset(next)
	}
}

// Process runs one step of gate id under its lock.
func (e *Engine) Process(ctx context.Context, id int64) {
	unlock := e.lock(id)
	defer unlock()
	g, err := e.st.GetGate(ctx, id)
	if err != nil {
		if err != store.ErrNotFound {
			e.log.Error("load gate", "gate", id, "error", err)
		}
		return
	}
	if !g.Open() || g.NextAt.After(e.now()) {
		return
	}
	e.step(ctx, g)
}

func (e *Engine) runDeletions(ctx context.Context, now time.Time) {
	dels, err := e.st.DueDeletions(ctx, now, 50)
	if err != nil {
		return
	}
	for _, d := range dels {
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var err error
		if d.UserID != 0 {
			err = e.tg.DeleteEphemeral(dctx, d.ChatID, d.UserID, d.MessageID)
		} else {
			err = e.tg.DeleteMessage(dctx, d.ChatID, d.MessageID)
		}
		cancel()
		if tg.IsTooManyRequests(err) || errors.Is(err, context.DeadlineExceeded) {
			return // try the rest next round
		}
		if err == nil || tg.IsPermanent(err) || now.Sub(d.Due) > time.Hour {
			_ = e.st.DoneDeletion(ctx, d)
		}
	}
}

// alert notifies the owner at most once per key per 10 minutes.
func (e *Engine) alert(ctx context.Context, key, text string, kb *tg.InlineKeyboard) {
	e.mu.Lock()
	if last, ok := e.alerts[key]; ok && e.now().Sub(last) < 10*time.Minute {
		e.mu.Unlock()
		return
	}
	e.alerts[key] = e.now()
	e.mu.Unlock()
	if _, err := e.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: e.cfg.OwnerID, Text: text, ReplyMarkup: kb}); err != nil {
		e.log.Warn("owner alert failed", "key", key, "error", err)
	}
}

func newToken() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func newSeed() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b
}

// DeepLink returns the private-chat start link for a gate.
func (e *Engine) DeepLink(g *store.Gate) string {
	return "https://t.me/" + e.cfg.BotUsername + "?start=v" + g.Token
}

// WebAppURL returns the Mini App URL for a gate.
func (e *Engine) WebAppURL(g *store.Gate) string {
	if e.cfg.WebURL == "" {
		return ""
	}
	return e.cfg.WebURL + "/v/" + g.Token
}

// once reports true the first time key is seen within ttl.
func (e *Engine) once(key string, ttl time.Duration) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if last, ok := e.alerts[key]; ok && now.Sub(last) < ttl {
		return false
	}
	if len(e.alerts) > 10000 {
		e.alerts = map[string]time.Time{}
	}
	e.alerts[key] = now
	return true
}
