// Package userbot drives an optional MTProto user account whose single job is
// force-refreshing Telegram link previews through @WebpageBot.
//
// A Manager keeps one gotd connection alive (Run), exposes a step-driven login
// API suitable for a chat UI or a CLI (QR code, phone code, 2FA password), and
// sends URLs to @WebpageBot (RefreshPreview).
package userbot

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"runtime"
	"sync"
	"time"

	"github.com/gotd/log/logslog"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// Defaults for Options and internal timings.
const (
	DefaultReplyTimeout = 25 * time.Second
	DefaultBotUsername  = "WebpageBot"

	connectWait       = 10 * time.Second // max wait for a connection in API calls
	startupGrace      = 2 * time.Second  // wait when Run has not started yet
	minBackoff        = time.Second
	maxBackoff        = 2 * time.Minute
	stableConnection  = time.Minute // a run lasting this long resets backoff
	sessionClearLimit = 10 * time.Second
)

// Options configure a Manager.
type Options struct {
	// APIID and APIHash come from https://my.telegram.org.
	APIID   int
	APIHash string
	// Logger receives package logs; gotd internals are logged at Warn and
	// above only. Defaults to slog.Default().
	Logger *slog.Logger

	// Device fields sent in initConnection; defaults are "TAdmin",
	// runtime.GOOS and "1.0".
	DeviceModel   string
	SystemVersion string
	AppVersion    string
	LangCode      string

	// ReplyTimeout bounds how long RefreshPreview waits for the bot reply.
	ReplyTimeout time.Duration
	// BotUsername is the preview bot; defaults to "WebpageBot".
	BotUsername string
}

// State is the coarse state of the user account connection.
type State string

// Manager states.
const (
	StateOffline    State = "offline"    // Run is not running
	StateConnecting State = "connecting" // establishing or re-establishing the connection
	StateLoggedOut  State = "logged_out" // connected, account not authorized
	StateReady      State = "ready"      // connected and authorized
)

// Self describes the logged-in account.
type Self struct {
	ID          int64
	Username    string
	FirstName   string
	LastName    string
	PhoneMasked string
}

// Status is a snapshot of the Manager state.
type Status struct {
	State     State
	Self      *Self
	LastError string
	// FloodUntil is set while Telegram flood control is in effect.
	FloodUntil time.Time
	// Login is the step the current login flow awaits ("" when none).
	Login LoginStep
}

// Manager owns the MTProto user connection. All methods are safe for
// concurrent use.
type Manager struct {
	opts    Options
	store   SessionStore
	log     *slog.Logger
	gotdLog *slog.Logger

	mu          sync.Mutex
	running     bool
	gen         uint64 // incremented for every gotd client instance
	client      *telegram.Client
	adapter     *sessionAdapter
	connected   bool
	authKnown   bool
	authorized  bool
	self        *Self
	lastErr     string
	floodUntil  time.Time
	sendFlood   time.Time // flood wait affecting RefreshPreview
	changed     chan struct{}
	login       *loginFlow
	loginSeq    uint64
	webpagePeer *tg.InputPeerUser

	loginToken chan struct{} // signalled by updateLoginToken
	restart    chan struct{} // asks the running client to restart

	refreshMu sync.Mutex // RefreshPreview is single-lane
}

// New creates a Manager. store may be nil for an ephemeral in-memory session.
func New(opts Options, store SessionStore) *Manager {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.DeviceModel == "" {
		opts.DeviceModel = "TAdmin"
	}
	if opts.SystemVersion == "" {
		opts.SystemVersion = runtime.GOOS
	}
	if opts.AppVersion == "" {
		opts.AppVersion = "1.0"
	}
	if opts.LangCode == "" {
		opts.LangCode = "en"
	}
	if opts.ReplyTimeout <= 0 {
		opts.ReplyTimeout = DefaultReplyTimeout
	}
	if opts.BotUsername == "" {
		opts.BotUsername = DefaultBotUsername
	}
	if store == nil {
		store = &memoryStore{}
	}
	log := opts.Logger.With("component", "userbot")
	return &Manager{
		opts:       opts,
		store:      store,
		log:        log,
		gotdLog:    slog.New(minLevelHandler{Handler: log.Handler(), min: slog.LevelWarn}).With("layer", "gotd"),
		changed:    make(chan struct{}),
		loginToken: make(chan struct{}, 1),
		restart:    make(chan struct{}, 1),
	}
}

// Status returns a snapshot of the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{State: m.stateLocked(), LastError: m.lastErr}
	if m.self != nil {
		self := *m.self
		st.Self = &self
	}
	if time.Now().Before(m.floodUntil) {
		st.FloodUntil = m.floodUntil
	}
	if m.login != nil {
		st.Login = m.login.step
	}
	return st
}

// Ready reports whether the account is connected and authorized.
func (m *Manager) Ready() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stateLocked() == StateReady
}

// Changed returns a channel that is closed on the next status change.
func (m *Manager) Changed() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.changed
}

// Run keeps the MTProto connection alive until ctx is done, reconnecting with
// capped exponential backoff. When the account is not authorized it stays
// connected in StateLoggedOut so login flows can run. It returns nil when ctx
// is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	if m.opts.APIID == 0 || m.opts.APIHash == "" {
		return errors.New("userbot: api_id and api_hash are required")
	}
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return errors.New("userbot: already running")
	}
	m.running = true
	m.notifyLocked()
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.running = false
		m.detachLocked()
		m.cancelLoginLocked()
		m.notifyLocked()
		m.mu.Unlock()
	}()

	delay := minBackoff
	for {
		started := time.Now()
		err := m.runOnce(ctx)
		m.mu.Lock()
		m.detachLocked()
		m.notifyLocked()
		m.mu.Unlock()
		if ctx.Err() != nil {
			return nil
		}
		var rs *restartSignal
		if errors.As(err, &rs) {
			delay = minBackoff
			continue
		}
		if err != nil {
			m.setLastError(err)
			m.log.Warn("connection ended", "err", err)
		}
		if time.Since(started) > stableConnection {
			delay = minBackoff
		}
		wait := delay/2 + rand.N(delay/2+1)
		m.log.Info("reconnecting", "in", wait)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		delay = min(delay*2, maxBackoff)
	}
}

// restartSignal makes runOnce return so Run starts a fresh client at once.
type restartSignal struct{}

func (*restartSignal) Error() string { return "userbot: restart requested" }

// runOnce runs a single gotd client until it fails or a restart is requested.
func (m *Manager) runOnce(ctx context.Context) error {
	adapter := &sessionAdapter{store: m.store}
	dispatcher := tg.NewUpdateDispatcher()
	dispatcher.OnLoginToken(func(context.Context, tg.Entities, *tg.UpdateLoginToken) error {
		select {
		case m.loginToken <- struct{}{}:
		default:
		}
		return nil
	})

	m.mu.Lock()
	m.gen++
	gen := m.gen
	m.adapter = adapter
	m.mu.Unlock()

	client := telegram.NewClient(m.opts.APIID, m.opts.APIHash, telegram.Options{
		SessionStorage: adapter,
		UpdateHandler:  dispatcher,
		Logger:         logslog.New(m.gotdLog),
		Device: telegram.DeviceConfig{
			DeviceModel:    m.opts.DeviceModel,
			SystemVersion:  m.opts.SystemVersion,
			AppVersion:     m.opts.AppVersion,
			SystemLangCode: m.opts.LangCode,
			LangCode:       m.opts.LangCode,
		},
		OnConnectionState: func(s telegram.ConnectionState) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.gen != gen {
				return
			}
			m.connected = s == telegram.ConnectionStateReady
			m.notifyLocked()
		},
	})

	return client.Run(ctx, func(ctx context.Context) error {
		// Drop restart requests issued before this client existed.
		select {
		case <-m.restart:
		default:
		}
		m.mu.Lock()
		m.client = client
		m.connected = true
		m.notifyLocked()
		m.mu.Unlock()

		st, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !st.Authorized && adapter.loaded.Load() {
			// The stored key is no longer bound to an account (logged out
			// elsewhere, revoked, ...): start over with a fresh key.
			m.log.Info("stored session is no longer authorized, clearing it")
			m.mu.Lock()
			err := m.clearStore(ctx, adapter)
			m.mu.Unlock()
			if err != nil {
				return err
			}
			return &restartSignal{}
		}

		m.mu.Lock()
		if m.gen == gen {
			m.authKnown = true
			m.authorized = st.Authorized
			m.self = nil
			if st.Authorized {
				m.self = selfFromUser(st.User)
				m.lastErr = ""
			}
			m.notifyLocked()
		}
		m.mu.Unlock()
		if st.Authorized {
			m.log.Info("user account ready", "user_id", st.User.ID)
		} else {
			m.log.Info("user account not logged in")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.restart:
			return &restartSignal{}
		}
	})
}

// requestRestart asks the running client (if any) to restart.
func (m *Manager) requestRestart() {
	select {
	case m.restart <- struct{}{}:
	default:
	}
}

// clearStore disables adapter (so its client cannot write the old session
// back) and clears the persisted session. m.mu must be held.
func (m *Manager) clearStore(ctx context.Context, adapter *sessionAdapter) error {
	if adapter != nil {
		adapter.writeMu.Lock()
		defer adapter.writeMu.Unlock()
		adapter.disabled.Store(true)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionClearLimit)
	defer cancel()
	if err := m.store.StoreSession(ctx, nil); err != nil {
		m.lastErr = err.Error()
		m.notifyLocked()
		return err
	}
	return nil
}

// stateLocked derives the State; m.mu must be held.
func (m *Manager) stateLocked() State {
	switch {
	case !m.running:
		return StateOffline
	case m.client == nil || !m.connected || !m.authKnown:
		return StateConnecting
	case m.authorized:
		return StateReady
	default:
		return StateLoggedOut
	}
}

// detachLocked forgets the current client; m.mu must be held.
func (m *Manager) detachLocked() {
	m.cancelLoginLocked()
	m.client = nil
	m.connected = false
	m.authKnown = false
}

// notifyLocked wakes Changed() waiters; m.mu must be held.
func (m *Manager) notifyLocked() {
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *Manager) setLastError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastErr = err.Error()
	m.notifyLocked()
}

// waitConnected waits (bounded) for an initialized connection with known
// authorization state and returns the client and state.
func (m *Manager) waitConnected(ctx context.Context) (*telegram.Client, State, error) {
	deadline := time.Now().Add(connectWait)
	m.mu.Lock()
	if !m.running {
		deadline = time.Now().Add(startupGrace)
	}
	m.mu.Unlock()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		m.mu.Lock()
		st, client, ch := m.stateLocked(), m.client, m.changed
		m.mu.Unlock()
		if st == StateReady || st == StateLoggedOut {
			return client, st, nil
		}
		select {
		case <-ctx.Done():
			return nil, st, ctx.Err()
		case <-timer.C:
			return nil, st, ErrNotReady
		case <-ch:
		}
	}
}

// observe maps err and applies state side effects (flood wait bookkeeping,
// revoked sessions).
func (m *Manager) observe(api *tg.Client, err error) error {
	mapped := mapError(err)
	if mapped == nil {
		return nil
	}
	var fw *FloodWaitError
	if errors.As(mapped, &fw) {
		m.mu.Lock()
		if until := time.Now().Add(fw.Wait); until.After(m.floodUntil) {
			m.floodUntil = until
		}
		m.notifyLocked()
		m.mu.Unlock()
		m.log.Warn("flood wait", "wait", fw.Wait)
	}
	if errors.Is(mapped, ErrAuthLost) {
		m.onAuthLost(api, mapped)
	}
	return mapped
}

// onAuthLost handles a revoked authorization of a ready account.
func (m *Manager) onAuthLost(api *tg.Client, cause error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.authorized || m.client == nil || api != m.client.API() {
		return
	}
	m.authorized = false
	m.self = nil
	m.webpagePeer = nil
	m.lastErr = cause.Error()
	adapter := m.adapter
	m.notifyLocked()

	m.log.Warn("user session revoked, clearing it", "err", cause)
	_ = m.clearStore(context.Background(), adapter)
	m.requestRestart()
}

// selfFromUser converts a tg.User.
func selfFromUser(u *tg.User) *Self {
	if u == nil {
		return nil
	}
	return &Self{
		ID:          u.ID,
		Username:    u.Username,
		FirstName:   u.FirstName,
		LastName:    u.LastName,
		PhoneMasked: MaskPhone(u.Phone),
	}
}

// minLevelHandler drops records below min; used to keep gotd quiet.
type minLevelHandler struct {
	slog.Handler
	min slog.Level
}

func (h minLevelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func (h minLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return minLevelHandler{Handler: h.Handler.WithAttrs(attrs), min: h.min}
}

func (h minLevelHandler) WithGroup(name string) slog.Handler {
	return minLevelHandler{Handler: h.Handler.WithGroup(name), min: h.min}
}

// memoryStore is the default SessionStore: the session lives only in memory.
type memoryStore struct {
	mu   sync.Mutex
	data []byte
}

func (s *memoryStore) LoadSession(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.data...), nil
}

func (s *memoryStore) StoreSession(_ context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = append([]byte(nil), data...)
	return nil
}
