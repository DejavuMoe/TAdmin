package intel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Defaults taken from the TypeSafe docs (API reference, Models, and SDK
// constants pages).
const (
	// DefaultBaseURL is the TypeSafe API origin.
	DefaultBaseURL = "https://api.typesafe.ai"
	// DefaultModel is the flagship-model alias. Pin a versioned ID such as
	// "jev-1.13.0" once thresholds have been tuned against it.
	DefaultModel = "jev-latest"
	// DefaultTimeout bounds one assessment; Jev typically answers in about
	// 100 ms, and joins must not wait long on an advisory signal.
	DefaultTimeout = 4 * time.Second

	systemOnePath   = "/v1/systemone"
	maxResponseBody = 1 << 20
)

// NewTypeSafe returns an Assessor backed by the TypeSafe System One HTTP API.
// It makes exactly one request per Assess call and does not retry; callers
// decide whether an advisory signal is worth waiting for (see RetryAfter).
func NewTypeSafe(opts Options) (Assessor, error) {
	key := strings.TrimSpace(opts.APIKey)
	if key == "" {
		return nil, ErrNoAPIKey
	}
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("intel: invalid typesafe base url %q", base)
	}
	u.RawQuery, u.Fragment = "", ""
	endpoint := strings.TrimRight(u.String(), "/") + systemOnePath

	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = DefaultModel
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &typeSafe{
		apiKey:   key,
		endpoint: endpoint,
		model:    model,
		timeout:  timeout,
		client:   client,
		now:      time.Now,
	}, nil
}

type typeSafe struct {
	apiKey   string
	endpoint string
	model    string
	timeout  time.Duration
	client   *http.Client
	now      func() time.Time
}

// String hides the API key if the assessor is ever printed.
func (t *typeSafe) String() string {
	return "intel.TypeSafe{endpoint:" + t.endpoint + " model:" + t.model + "}"
}

// GoString hides the API key for %#v as well.
func (t *typeSafe) GoString() string { return t.String() }

// Wire types for POST /v1/systemone.

type systemOneRequest struct {
	State     requestState            `json:"state"`
	Model     string                  `json:"model"`
	Questions map[string]noulQuestion `json:"questions"`
}

type systemOneResponse struct {
	Model   string                `json:"model"`
	Answers map[string]wireAnswer `json:"answers"`
}

type wireAnswer struct {
	Type string   `json:"type"`
	Noul *float64 `json:"noul"`
}

// Assess asks the whole question battery in one request and composes Risk.
// Errors never carry the API key or profile contents.
func (t *typeSafe) Assess(ctx context.Context, p Profile) (Assessment, error) {
	state := buildState(p)
	if !state.hasText() {
		// Nothing to judge; an absent profile is not evidence of spam.
		return Assessment{Signals: map[string]float64{}}, nil
	}

	body, err := json.Marshal(systemOneRequest{State: state, Model: t.model, Questions: questions})
	if err != nil {
		return Assessment{}, fmt.Errorf("intel: encode typesafe request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return Assessment{}, fmt.Errorf("intel: build typesafe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	start := t.now()
	resp, err := t.client.Do(req)
	if err != nil {
		return Assessment{}, t.transportError(ctx, callCtx, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
		return Assessment{}, &APIError{
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header, t.now()),
			RequestID:  strings.TrimSpace(resp.Header.Get("x-typesafe-request-id")),
		}
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return Assessment{}, t.transportError(ctx, callCtx, err)
	}
	latency := t.now().Sub(start)
	if len(raw) > maxResponseBody {
		return Assessment{}, fmt.Errorf("%w: body exceeds %d bytes", ErrMalformedResponse, maxResponseBody)
	}

	a, err := parseAssessment(raw)
	if err != nil {
		return Assessment{}, err
	}
	if a.Model == "" {
		a.Model = t.model
	}
	a.Latency = latency
	return a, nil
}

// transportError classifies a failed round trip without exposing the request.
func (t *typeSafe) transportError(parent, call context.Context, err error) error {
	if perr := parent.Err(); perr != nil {
		return fmt.Errorf("intel: typesafe request canceled: %w", perr)
	}
	if errors.Is(call.Err(), context.DeadlineExceeded) || isNetTimeout(err) {
		return fmt.Errorf("%w after %s: %w", ErrTimeout, t.timeout, context.DeadlineExceeded)
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		// Drop the URL wrapper text; keep the underlying cause.
		err = uerr.Err
	}
	return fmt.Errorf("intel: typesafe request failed: %w", err)
}

func isNetTimeout(err error) bool {
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}

// parseAssessment validates a documented response body and composes Risk as
// the maximum of the serious signals.
func parseAssessment(raw []byte) (Assessment, error) {
	var resp systemOneResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Assessment{}, fmt.Errorf("%w: %v", ErrMalformedResponse, jsonErrorKind(err))
	}
	if resp.Answers == nil {
		return Assessment{}, fmt.Errorf("%w: missing answers", ErrMalformedResponse)
	}
	signals := make(map[string]float64, len(questions))
	for id := range questions {
		ans, ok := resp.Answers[id]
		if !ok {
			return Assessment{}, fmt.Errorf("%w: missing answer %q", ErrMalformedResponse, id)
		}
		if ans.Type != "noul" {
			return Assessment{}, fmt.Errorf("%w: answer %q has type %q", ErrMalformedResponse, id, ans.Type)
		}
		if ans.Noul == nil {
			return Assessment{}, fmt.Errorf("%w: answer %q has no value", ErrMalformedResponse, id)
		}
		v := *ans.Noul
		if math.IsNaN(v) || v < 0 || v > 1 {
			return Assessment{}, fmt.Errorf("%w: answer %q out of range", ErrMalformedResponse, id)
		}
		signals[id] = v
	}

	a := Assessment{Signals: signals, Model: strings.TrimSpace(resp.Model)}
	a.Risk = -1
	for _, id := range riskSignals {
		if v := signals[id]; v > a.Risk {
			a.Risk, a.Reason = v, id
		}
	}
	return a, nil
}

// jsonErrorKind describes a decode failure without quoting body content.
func jsonErrorKind(err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		return fmt.Sprintf("invalid json at offset %d", syn.Offset)
	case errors.As(err, &typ):
		return fmt.Sprintf("unexpected %s for field %q", typ.Value, typ.Field)
	default:
		return "invalid json"
	}
}
