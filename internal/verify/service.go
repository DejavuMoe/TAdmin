package verify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"tadmin/internal/store"
)

const (
	DefaultLifetime  = 180 * time.Second
	MaxAttempts      = 3
	SourceGuardQuery = "guard_query"
)

var (
	ErrInvalidSession       = errors.New("invalid verification session")
	ErrSessionIdentity      = errors.New("verification session identity mismatch")
	ErrPublicURLUnavailable = errors.New("public_base_url is required to create a verification URL")
)

type Service struct {
	store       *store.Store
	botToken    string
	now         func() time.Time
	randomBytes func([]byte) error
	lifetime    time.Duration
	initMaxAge  time.Duration
	futureSkew  time.Duration
}

type CreateParams struct {
	ChatID     int64
	UserID     int64
	SourceKind string
	SourceRef  string
	Language   string
	Lifetime   time.Duration
}

type View struct {
	Status            string
	Language          string
	Prompt            string
	Generation        int
	ExpiresAt         time.Time
	RemainingAttempts int
}

type AnswerResult struct {
	View    View
	Correct bool
}

func NewService(database *store.Store, botToken string) *Service {
	return &Service{
		store:       database,
		botToken:    botToken,
		now:         time.Now,
		randomBytes: randomFill,
		lifetime:    DefaultLifetime,
		initMaxAge:  DefaultInitDataMaxAge,
		futureSkew:  DefaultFutureSkew,
	}
}

func (s *Service) CreateSession(ctx context.Context, params CreateParams) (string, error) {
	token, _, err := s.CreateOrGetSession(ctx, params)
	return token, err
}

// CreateOrGetSession gives join-update replay a stable challenge. Stage 3 calls
// it only after atomically reserving the join case's unique source key.
func (s *Service) CreateOrGetSession(ctx context.Context, params CreateParams) (string, bool, error) {
	if s == nil || s.store == nil || params.ChatID == 0 || params.UserID <= 0 ||
		strings.TrimSpace(params.SourceKind) == "" || strings.TrimSpace(params.SourceRef) == "" {
		return "", false, ErrInvalidSession
	}
	sourceKind := strings.TrimSpace(params.SourceKind)
	sourceRef := strings.TrimSpace(params.SourceRef)
	if existing, err := s.store.GetVerificationSessionBySource(ctx, params.ChatID, sourceKind, sourceRef); err == nil {
		if existing.UserID != params.UserID {
			return "", false, ErrSessionIdentity
		}
		return existing.ID, false, nil
	} else if !errors.Is(err, store.ErrVerificationNotFound) {
		return "", false, err
	}
	language := normalizeLanguage(params.Language)
	tokenBytes := make([]byte, 32)
	secret := make([]byte, 32)
	if err := s.randomBytes(tokenBytes); err != nil {
		return "", false, fmt.Errorf("create verification token: %w", err)
	}
	if err := s.randomBytes(secret); err != nil {
		return "", false, fmt.Errorf("create verification challenge: %w", err)
	}
	now := s.currentTime()
	lifetime := params.Lifetime
	if lifetime == 0 {
		lifetime = s.lifetime
	}
	if lifetime < 30*time.Second || lifetime > 15*time.Minute {
		return "", false, ErrInvalidSession
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	session := store.VerificationSession{
		ID:              token,
		ChatID:          params.ChatID,
		UserID:          params.UserID,
		SourceKind:      sourceKind,
		SourceRef:       sourceRef,
		Language:        language,
		Status:          store.VerificationChallengeIssued,
		Generation:      1,
		ChallengeSecret: secret,
		ExpiresAt:       now.Add(lifetime),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.store.CreateVerificationSession(ctx, session); err != nil {
		if existing, getErr := s.store.GetVerificationSessionBySource(ctx, params.ChatID, sourceKind, sourceRef); getErr == nil {
			if existing.UserID != params.UserID {
				return "", false, ErrSessionIdentity
			}
			return existing.ID, false, nil
		}
		return "", false, err
	}
	return token, true, nil
}

func (s *Service) Open(ctx context.Context, token, rawInitData string) (View, error) {
	identity, err := s.validateIdentity(rawInitData)
	if err != nil {
		return View{}, err
	}
	session, err := s.store.GetActiveVerificationSession(ctx, token, s.currentTime())
	if err != nil {
		return View{}, err
	}
	if err := validateSessionIdentity(session, token, identity); err != nil {
		return View{}, err
	}
	return activeView(session)
}

func (s *Service) Image(ctx context.Context, token string, generation int) ([]byte, error) {
	session, err := s.store.GetActiveVerificationSession(ctx, token, s.currentTime())
	if err != nil {
		return nil, err
	}
	if generation != session.Generation {
		return nil, store.ErrVerificationStale
	}
	challenge, err := NewChallenge(session.ChallengeSecret, session.Generation)
	if err != nil {
		return nil, ErrInvalidSession
	}
	return challenge.PNG()
}

func (s *Service) Answer(ctx context.Context, token, rawInitData string, generation int, points []Point) (AnswerResult, error) {
	identity, err := s.validateIdentity(rawInitData)
	if err != nil {
		return AnswerResult{}, err
	}
	now := s.currentTime()
	session, err := s.store.GetActiveVerificationSession(ctx, token, now)
	if err != nil {
		return AnswerResult{}, err
	}
	if err := validateSessionIdentity(session, token, identity); err != nil {
		return AnswerResult{}, err
	}
	if generation != session.Generation {
		return AnswerResult{}, store.ErrVerificationStale
	}
	challenge, err := NewChallenge(session.ChallengeSecret, session.Generation)
	if err != nil {
		return AnswerResult{}, ErrInvalidSession
	}
	correct, err := challenge.Check(points)
	if err != nil {
		return AnswerResult{}, err
	}

	var replacementSecret []byte
	if !correct && session.Attempts+1 < MaxAttempts {
		replacementSecret = make([]byte, 32)
		if err := s.randomBytes(replacementSecret); err != nil {
			return AnswerResult{}, fmt.Errorf("rotate verification challenge: %w", err)
		}
	}
	transition, err := s.store.SubmitVerification(ctx, token, generation, correct, replacementSecret, MaxAttempts, now)
	if err != nil {
		return AnswerResult{}, err
	}
	if transition.Session.Status == store.VerificationChallengeIssued {
		view, err := activeView(transition.Session)
		if err != nil {
			return AnswerResult{}, err
		}
		return AnswerResult{View: view, Correct: false}, nil
	}
	return AnswerResult{
		Correct: correct,
		View: View{
			Status:            transition.Session.Status,
			Language:          normalizeLanguage(transition.Session.Language),
			ExpiresAt:         transition.Session.ExpiresAt,
			RemainingAttempts: max(0, MaxAttempts-transition.Session.Attempts),
		},
	}, nil
}

func VerificationURL(publicBaseURL, token string) (string, error) {
	if strings.TrimSpace(publicBaseURL) == "" {
		return "", ErrPublicURLUnavailable
	}
	parsed, err := url.Parse(publicBaseURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrPublicURLUnavailable
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/verify/" + url.PathEscape(token)
	parsed.RawPath = ""
	return parsed.String(), nil
}

func (s *Service) validateIdentity(raw string) (InitData, error) {
	if s == nil || strings.TrimSpace(s.botToken) == "" {
		return InitData{}, ErrInitDataInvalid
	}
	return ValidateInitData(raw, s.botToken, s.currentTime(), s.initMaxAge, s.futureSkew)
}

func validateSessionIdentity(session store.VerificationSession, token string, identity InitData) error {
	if identity.UserID != session.UserID {
		return ErrSessionIdentity
	}
	if identity.StartParam != "" && identity.StartParam != token && identity.StartParam != "verify_"+token {
		return ErrSessionIdentity
	}
	if session.SourceKind == SourceGuardQuery && identity.QueryID != "" && identity.QueryID != session.SourceRef {
		return ErrSessionIdentity
	}
	return nil
}

func activeView(session store.VerificationSession) (View, error) {
	challenge, err := NewChallenge(session.ChallengeSecret, session.Generation)
	if err != nil {
		return View{}, ErrInvalidSession
	}
	return View{
		Status:            session.Status,
		Language:          normalizeLanguage(session.Language),
		Prompt:            challenge.Prompt(session.Language),
		Generation:        session.Generation,
		ExpiresAt:         session.ExpiresAt,
		RemainingAttempts: max(0, MaxAttempts-session.Attempts),
	}, nil
}

func normalizeLanguage(language string) string {
	if language == "en" {
		return "en"
	}
	return "zh-CN"
}

func (s *Service) currentTime() time.Time {
	return s.now().UTC().Truncate(time.Second)
}

func randomFill(target []byte) error {
	_, err := rand.Read(target)
	return err
}
