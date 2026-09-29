// Package intel provides an optional, advisory risk assessment of the public
// profile of an account that is joining a group.
//
// The assessment is produced by TypeSafe's System One model (Jev) through a
// handful of independent yes/no (Noul) judgments asked in one request. It is
// strictly advisory: callers may use it to escalate handling (for example to
// require manual review or a stricter challenge) and to show a hint on an
// admin review card. It must never be used to approve, ban, or remove anyone
// on its own, and the bot must keep working when it is disabled or failing.
//
// Only the joining account's own public profile text and the group's title and
// description are sent. Member messages are never inspected, and Telegram user
// IDs are never sent to the API.
package intel

import (
	"context"
	"math"
	"net/http"
	"time"
)

// EnvAPIKey is the environment variable conventionally holding the TypeSafe
// API key. The feature is disabled when it is unset.
const EnvAPIKey = "TADMIN_TYPESAFE_API_KEY"

// Profile is the public information available about a joining account plus
// the context of the group being joined.
type Profile struct {
	// UserID identifies the account locally. It is never sent to the API.
	UserID       int64
	FirstName    string
	LastName     string
	Username     string
	Bio          string
	LanguageCode string
	IsPremium    bool
	// HasPhoto is nil when it is unknown whether the account has a photo.
	HasPhoto   *bool
	ViaRequest bool
	InviteName string

	ChatTitle       string
	ChatDescription string
}

// Assessment is the typed result of one assessment request.
type Assessment struct {
	// Risk is the probability-like value in [0, 1] that the profile belongs to
	// a spam, advertising, or scam account. It is the maximum of the serious
	// signals (see RiskSignals): an "any serious violation" rule.
	Risk float64
	// Signals holds every raw Noul answer keyed by signal ID (see the Signal*
	// constants). They are kept so policy can change without re-asking.
	Signals map[string]float64
	// Reason is the ID of the serious signal with the highest value. It is a
	// fixed label chosen by code, never model-generated text. It is empty when
	// no request was made (profile without any text).
	Reason string
	// Model is the versioned model ID reported by the API.
	Model string
	// Latency is the wall-clock duration of the API round trip.
	Latency time.Duration
}

// Assessor produces an Assessment for a profile.
type Assessor interface {
	Assess(ctx context.Context, p Profile) (Assessment, error)
}

// Options configures the TypeSafe-backed Assessor.
type Options struct {
	// APIKey is required. It is sent only as a bearer token.
	APIKey string
	// BaseURL defaults to DefaultBaseURL.
	BaseURL string
	// Model defaults to DefaultModel.
	Model string
	// Timeout bounds each Assess call; it defaults to DefaultTimeout.
	Timeout time.Duration
	// HTTPClient defaults to a new client without its own timeout; the
	// per-call Timeout above applies through the request context.
	HTTPClient *http.Client
}

// String redacts the API key so Options can be logged safely.
func (o Options) String() string {
	key := "<unset>"
	if o.APIKey != "" {
		key = "<redacted>"
	}
	return "intel.Options{APIKey:" + key + " BaseURL:" + o.BaseURL + " Model:" + o.Model + " Timeout:" + o.Timeout.String() + "}"
}

// GoString redacts the API key for %#v as well.
func (o Options) GoString() string { return o.String() }

// Level is a coarse handling level derived from an Assessment by policy.
type Level int

const (
	// LevelNone means no escalation is suggested.
	LevelNone Level = iota
	// LevelElevated suggests a stricter path, e.g. a harder challenge.
	LevelElevated
	// LevelHigh suggests manual review.
	LevelHigh
)

// String returns a stable lowercase name for the level.
func (l Level) String() string {
	switch l {
	case LevelElevated:
		return "elevated"
	case LevelHigh:
		return "high"
	default:
		return "none"
	}
}

// Default thresholds for Classify. They are starting points only and must be
// evaluated against real, labelled join data before being relied upon: Jev's
// Noul values are calibrated probabilities, and the right cut-offs depend on
// the cost of a false escalation versus a missed spammer in each group.
const (
	DefaultElevated = 0.6
	DefaultHigh     = 0.85
)

// Classify maps an assessment's Risk onto a Level. Non-positive or NaN
// thresholds fall back to the defaults; elevated is capped at high. It never
// returns anything that implies approval: LevelNone only means "no extra
// scrutiny suggested".
func Classify(a Assessment, elevated, high float64) Level {
	if !(high > 0) {
		high = DefaultHigh
	}
	if !(elevated > 0) {
		elevated = DefaultElevated
	}
	if elevated > high {
		elevated = high
	}
	r := a.Risk
	if math.IsNaN(r) {
		return LevelNone
	}
	switch {
	case r >= high:
		return LevelHigh
	case r >= elevated:
		return LevelElevated
	default:
		return LevelNone
	}
}
