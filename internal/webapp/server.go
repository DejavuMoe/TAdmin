// Package webapp serves the optional verification Mini App and a health
// endpoint. It listens on loopback behind the operator's HTTPS proxy.
package webapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	web "tadmin/assets/web"
	"tadmin/internal/gate"
)

// Engine is the part of the gate engine the Mini App uses.
type Engine interface {
	WebOpen(ctx context.Context, token string, userID int64, queryID string) (*gate.View, error)
	WebAnswer(ctx context.Context, token string, userID int64, queryID string, gen int, answer []int) (*gate.View, error)
	WebHelp(ctx context.Context, token string, userID int64, queryID string) (*gate.View, error)
}

// Config wires the server.
type Config struct {
	BotToken   string
	Engine     Engine
	Health     func(ctx context.Context) error
	Version    string
	EnableApp  bool
	Logger     *slog.Logger
	Now        func() time.Time
	MaxInitAge time.Duration
}

// Handler returns the HTTP handler.
func Handler(cfg Config) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxInitAge == 0 {
		cfg.MaxInitAge = time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	s := &server{cfg: cfg, limiter: newLimiter(40, time.Minute), render: make(chan struct{}, 4)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	if cfg.EnableApp {
		mux.HandleFunc("GET /v/{token}", s.page)
		mux.HandleFunc("GET /assets/{name}", s.asset)
		mux.HandleFunc("POST /api/v/{token}/{action}", s.api)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return secure(mux)
}

type server struct {
	cfg     Config
	limiter *limiter
	render  chan struct{}
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self' https://telegram.org; style-src 'self'; "+
			"img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; "+
			"frame-ancestors https://web.telegram.org https://*.telegram.org https://telegram.org")
		next.ServeHTTP(w, r)
	})
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.Health != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.cfg.Health(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": s.cfg.Version})
}

func (s *server) page(w http.ResponseWriter, r *http.Request) {
	if !validToken(r.PathValue("token")) {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(web.FS, "index.html")
	if err != nil {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

var assetTypes = map[string]string{".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8"}

func (s *server) asset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ct, ok := assetTypes[path.Ext(name)]
	if !ok || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(web.FS, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}

func validToken(tok string) bool {
	if len(tok) < 16 || len(tok) > 64 {
		return false
	}
	for _, c := range tok {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

type apiRequest struct {
	InitData string `json:"init_data"`
	Gen      int    `json:"gen"`
	Answer   []int  `json:"answer"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func (s *server) api(w http.ResponseWriter, r *http.Request) {
	token, action := r.PathValue("token"), r.PathValue("action")
	if !validToken(token) {
		apiError(w, http.StatusNotFound, "invalid")
		return
	}
	// Per-token limit: a real user needs a handful of requests; the proxy
	// in front applies per-client limits.
	if !s.limiter.allow(token) {
		apiError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	var req apiRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<10))
	if err != nil || json.Unmarshal(body, &req) != nil || len(req.Answer) > 9 {
		apiError(w, http.StatusBadRequest, "invalid")
		return
	}
	init, err := ValidateInitData(req.InitData, s.cfg.BotToken, s.cfg.Now(), s.cfg.MaxInitAge)
	if err != nil {
		apiError(w, http.StatusForbidden, "forbidden")
		return
	}
	// Bound concurrent PNG renders.
	select {
	case s.render <- struct{}{}:
		defer func() { <-s.render }()
	case <-r.Context().Done():
		return
	}
	var view *gate.View
	switch action {
	case "open":
		view, err = s.cfg.Engine.WebOpen(r.Context(), token, init.UserID, init.GuardQueryID)
	case "answer":
		view, err = s.cfg.Engine.WebAnswer(r.Context(), token, init.UserID, init.GuardQueryID, req.Gen, req.Answer)
	case "help":
		view, err = s.cfg.Engine.WebHelp(r.Context(), token, init.UserID, init.GuardQueryID)
	default:
		apiError(w, http.StatusNotFound, "invalid")
		return
	}
	switch {
	case errors.Is(err, gate.ErrForbidden):
		apiError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, gate.ErrExpired):
		apiError(w, http.StatusGone, "expired")
	case errors.Is(err, gate.ErrStale):
		apiError(w, http.StatusConflict, "stale")
	case err != nil:
		if !errors.Is(err, gate.ErrInvalid) {
			s.cfg.Logger.Warn("mini app request failed", "action", action, "error", err)
		}
		apiError(w, http.StatusNotFound, "invalid")
	default:
		writeJSON(w, http.StatusOK, view)
	}
}

// limiter is a fixed-window counter per key.
type limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	start  time.Time
	counts map[string]int
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, start: time.Now(), counts: map[string]int{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.start) > l.window {
		l.start, l.counts = time.Now(), map[string]int{}
	}
	l.counts[key]++
	return l.counts[key] <= l.limit
}
