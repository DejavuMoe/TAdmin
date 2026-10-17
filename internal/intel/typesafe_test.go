package intel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testKey = "ts-test-key-123"

func spamProfile() Profile {
	photo := false
	return Profile{
		UserID:          987654321,
		FirstName:       "USDT承兑",
		LastName:        "加V abc123",
		Username:        "usdt_otc_fast",
		Bio:             "代充 代付 跑分 日赚千元 t.me/fastmoney",
		LanguageCode:    "zh-hans",
		HasPhoto:        &photo,
		ViaRequest:      true,
		InviteName:      "twitter-campaign",
		ChatTitle:       "Go 语言交流",
		ChatDescription: "讨论 Go 编程",
	}
}

func answers(values map[string]float64) map[string]any {
	out := map[string]any{}
	for id, v := range values {
		out[id] = map[string]any{"type": "noul", "noul": v}
	}
	return out
}

func okBody(values map[string]float64) string {
	b, _ := json.Marshal(map[string]any{
		"model":   "jev-1.13.0",
		"answers": answers(values),
		"usage":   map[string]int{"input_tokens": 900, "output_tokens": 50},
	})
	return string(b)
}

func allSignals(v float64) map[string]float64 {
	m := map[string]float64{}
	for id := range questions {
		m[id] = v
	}
	return m
}

func newTestAssessor(t *testing.T, srv *httptest.Server, timeout time.Duration) Assessor {
	t.Helper()
	a, err := NewTypeSafe(Options{APIKey: testKey, BaseURL: srv.URL + "/", Timeout: timeout, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAssessRequestShapeAndParsing(t *testing.T) {
	values := allSignals(0.05)
	values[SignalScam] = 0.97
	values[SignalPromotion] = 0.91
	values[SignalAdFormatting] = 0.99 // informational: must not drive Risk

	var gotBody map[string]any
	var rawBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("authorization header = %q", got)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		b, _ := io.ReadAll(r.Body)
		rawBody = string(b)
		if err := json.Unmarshal(b, &gotBody); err != nil {
			t.Errorf("request is not json: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody(values))
	}))
	defer srv.Close()

	a, err := newTestAssessor(t, srv, 0).Assess(context.Background(), spamProfile())
	if err != nil {
		t.Fatal(err)
	}

	// Request shape.
	if gotBody["model"] != DefaultModel {
		t.Errorf("model = %v", gotBody["model"])
	}
	if _, ok := gotBody["max_len"]; ok {
		t.Error("Laya budget sent to Jev")
	}
	if _, ok := gotBody["head_max_len"]; ok {
		t.Error("Laya head budget sent to Jev")
	}
	if a.HintOnly {
		t.Error("Jev unexpectedly forced to hint-only")
	}
	qs, _ := gotBody["questions"].(map[string]any)
	if len(qs) != len(questions) {
		t.Errorf("questions = %d, want %d", len(qs), len(questions))
	}
	for id := range questions {
		q, _ := qs[id].(map[string]any)
		if q["type"] != "noul" || q["instructions"] == nil || q["criteria"] == nil {
			t.Errorf("question %q malformed: %v", id, q)
		}
	}
	state, _ := gotBody["state"].(map[string]any)
	acct, _ := state["joining_account"].(map[string]any)
	if acct["bio"] != spamProfile().Bio || acct["username"] != "usdt_otc_fast" {
		t.Errorf("state account = %v", acct)
	}
	group, _ := state["group"].(map[string]any)
	if group["title"] != "Go 语言交流" {
		t.Errorf("state group = %v", group)
	}
	for _, leak := range []string{"987654321", "user_id", "twitter-campaign", "zh-hans", testKey} {
		if strings.Contains(rawBody, leak) {
			t.Errorf("request body contains %q", leak)
		}
	}

	// Response parsing and composition.
	if a.Risk != 0.97 || a.Reason != SignalScam {
		t.Errorf("risk=%v reason=%q", a.Risk, a.Reason)
	}
	if a.Signals[SignalAdFormatting] != 0.99 || len(a.Signals) != len(questions) {
		t.Errorf("signals = %v", a.Signals)
	}
	if a.Model != "jev-1.13.0" {
		t.Errorf("model = %q", a.Model)
	}
	if a.Latency <= 0 {
		t.Errorf("latency = %v", a.Latency)
	}
	if Classify(a, DefaultElevated, DefaultHigh) != LevelHigh {
		t.Error("expected high level")
	}
}

func TestAssessEmptyProfileSkipsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request expected")
	}))
	defer srv.Close()
	a, err := newTestAssessor(t, srv, 0).Assess(context.Background(), Profile{UserID: 1, FirstName: "  ", ChatTitle: "x"})
	if err != nil || a.Risk != 0 || a.Reason != "" {
		t.Fatalf("got %+v, %v", a, err)
	}
}

func TestAssessHTTPErrors(t *testing.T) {
	cases := []struct {
		status    int
		headers   map[string]string
		sentinel  error
		retry     time.Duration
		temporary bool
	}{
		{http.StatusUnauthorized, nil, ErrUnauthorized, 0, false},
		{http.StatusUnprocessableEntity, nil, ErrInvalidRequest, 0, false},
		{http.StatusTooManyRequests, map[string]string{"Retry-After": "2"}, ErrRateLimited, 2 * time.Second, true},
		{529, map[string]string{"retry-after-ms": "750"}, ErrRateLimited, 750 * time.Millisecond, true},
		{http.StatusInternalServerError, nil, ErrServer, 0, true},
	}
	for _, c := range cases {
		t.Run(strconv.Itoa(c.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range c.headers {
					w.Header().Set(k, v)
				}
				w.Header().Set("x-typesafe-request-id", "req_1")
				w.WriteHeader(c.status)
				// A body echoing request content must not surface in errors.
				_, _ = io.WriteString(w, `{"detail":"bad field joining_account.bio: 代充 代付"}`)
			}))
			defer srv.Close()

			_, err := newTestAssessor(t, srv, 0).Assess(context.Background(), spamProfile())
			if !errors.Is(err, c.sentinel) {
				t.Fatalf("err = %v, want %v", err, c.sentinel)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != c.status || apiErr.RequestID != "req_1" {
				t.Fatalf("err = %#v", err)
			}
			if apiErr.Temporary() != c.temporary {
				t.Errorf("temporary = %v", apiErr.Temporary())
			}
			d, ok := RetryAfter(err)
			if d != c.retry || ok != (c.retry > 0) {
				t.Errorf("retry after = %v, %v", d, ok)
			}
			msg := err.Error()
			if strings.Contains(msg, "代充") || strings.Contains(msg, testKey) {
				t.Errorf("error leaks content: %s", msg)
			}
			for _, other := range []error{ErrUnauthorized, ErrInvalidRequest, ErrRateLimited, ErrServer} {
				if other != c.sentinel && errors.Is(err, other) {
					t.Errorf("error also matches %v", other)
				}
			}
		})
	}
}

// blockingServer returns a server whose handler blocks until the test ends.
// The handler is released explicitly because client-side cancellation is not
// always observed by the server promptly on every platform.
func blockingServer(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	// Cleanups run last-in first-out: release handlers, then close.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv
}

func TestAssessTimeout(t *testing.T) {
	srv := blockingServer(t)
	start := time.Now()
	_, err := newTestAssessor(t, srv, 50*time.Millisecond).Assess(context.Background(), spamProfile())
	if !errors.Is(err, ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("timeout not enforced")
	}
}

func TestAssessParentCanceled(t *testing.T) {
	srv := blockingServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := newTestAssessor(t, srv, time.Second).Assess(ctx, spamProfile())
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
}

func TestAssessMalformedResponses(t *testing.T) {
	missing := allSignals(0.1)
	delete(missing, SignalAdult)
	wrongType := answers(allSignals(0.1))
	wrongType[SignalScam] = map[string]any{"type": "choice", "choice": "yes"}
	noValue := answers(allSignals(0.1))
	noValue[SignalScam] = map[string]any{"type": "noul"}
	outOfRange := allSignals(0.1)
	outOfRange[SignalScam] = 1.5
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }

	bodies := map[string]string{
		"not json":       `<html>oops</html>`,
		"truncated":      `{"model":"jev","answers":{`,
		"no answers":     `{"model":"jev-1.13.0"}`,
		"answers array":  `{"model":"jev-1.13.0","answers":[]}`,
		"missing answer": okBody(missing),
		"wrong type":     enc(map[string]any{"model": "m", "answers": wrongType}),
		"no value":       enc(map[string]any{"model": "m", "answers": noValue}),
		"string value":   strings.Replace(okBody(allSignals(0.1)), "0.1", `"0.1"`, 1),
		"out of range":   okBody(outOfRange),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()
			_, err := newTestAssessor(t, srv, 0).Assess(context.Background(), spamProfile())
			if !errors.Is(err, ErrMalformedResponse) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestAssessLowRiskAndModelFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(map[string]any{"answers": answers(allSignals(0.02))})
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	a, err := NewTypeSafe(Options{APIKey: testKey, BaseURL: srv.URL, Model: "jev-1.13.0", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Assess(context.Background(), Profile{FirstName: "Алексей", ChatTitle: "Go"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Risk != 0.02 || got.Model != "jev-1.13.0" || Classify(got, DefaultElevated, DefaultHigh) != LevelNone {
		t.Errorf("got %+v", got)
	}
}

func TestNewTypeSafeValidation(t *testing.T) {
	if _, err := NewTypeSafe(Options{}); !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("err = %v", err)
	}
	for _, base := range []string{"ftp://x", "not a url", "http://"} {
		if _, err := NewTypeSafe(Options{APIKey: "k", BaseURL: base}); err == nil {
			t.Errorf("base %q accepted", base)
		}
	}
	a, err := NewTypeSafe(Options{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	ts := a.(*typeSafe)
	if ts.endpoint != DefaultBaseURL+"/v1/systemone" || ts.model != DefaultModel || ts.timeout != DefaultTimeout {
		t.Errorf("defaults = %s %s %s", ts.endpoint, ts.model, ts.timeout)
	}
	a, _ = NewTypeSafe(Options{APIKey: "k", BaseURL: "https://proxy.example/typesafe/"})
	if got := a.(*typeSafe).endpoint; got != "https://proxy.example/typesafe/v1/systemone" {
		t.Errorf("endpoint = %s", got)
	}
}

func TestTransportErrorDoesNotLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // connection refused
	a, _ := NewTypeSafe(Options{APIKey: testKey, BaseURL: url})
	_, err := a.Assess(context.Background(), spamProfile())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "代充") {
		t.Errorf("error leaks: %v", err)
	}
}
