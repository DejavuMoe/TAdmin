package webapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tadmin/internal/gate"
)

const token = "123:TEST"

type fakeEngine struct {
	gotUser  int64
	gotQuery string
	err      error
}

func (f *fakeEngine) WebOpen(ctx context.Context, tok string, user int64, q string) (*gate.View, error) {
	f.gotUser, f.gotQuery = user, q
	return &gate.View{Status: gate.StatusChallenge, Gen: 1}, f.err
}

func (f *fakeEngine) WebAnswer(ctx context.Context, tok string, user int64, q string, gen int, ans []int) (*gate.View, error) {
	if gen != 1 {
		return nil, gate.ErrStale
	}
	return &gate.View{Status: gate.StatusPassed}, f.err
}

func (f *fakeEngine) WebHelp(ctx context.Context, tok string, user int64, q string) (*gate.View, error) {
	return &gate.View{Status: gate.StatusReview}, f.err
}

func post(t *testing.T, h http.Handler, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestInitDataValidation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	raw := SignInitData(token, 42, now, map[string]string{"chat_join_request_query_id": "q1"})
	d, err := ValidateInitData(raw, token, now, time.Hour)
	if err != nil || d.UserID != 42 || d.GuardQueryID != "q1" {
		t.Fatalf("valid init data rejected: %+v %v", d, err)
	}
	if _, err := ValidateInitData(raw, "other:token", now, time.Hour); err == nil {
		t.Fatal("wrong token accepted")
	}
	if _, err := ValidateInitData(raw, token, now.Add(2*time.Hour), time.Hour); err == nil {
		t.Fatal("expired init data accepted")
	}
	if _, err := ValidateInitData(strings.Replace(raw, "42", "43", 1), token, now, time.Hour); err == nil {
		t.Fatal("tampered init data accepted")
	}
}

func TestAPI(t *testing.T) {
	eng := &fakeEngine{}
	h := Handler(Config{BotToken: token, Engine: eng, EnableApp: true, Version: "t"})
	tok := "abcdefghijklmnopqrstuv"
	init := SignInitData(token, 42, time.Now(), map[string]string{"chat_join_request_query_id": "gq"})
	body := func(extra string) string {
		b, _ := json.Marshal(init)
		return `{"init_data":` + string(b) + extra + `}`
	}
	code, out := post(t, h, "/api/v/"+tok+"/open", body(""))
	if code != 200 || out["status"] != "challenge" || eng.gotUser != 42 || eng.gotQuery != "gq" {
		t.Fatalf("open: %d %v user %d", code, out, eng.gotUser)
	}
	if code, out = post(t, h, "/api/v/"+tok+"/answer", body(`,"gen":2,"answer":[1,2,3]`)); code != 409 || out["error"] != "stale" {
		t.Fatalf("stale: %d %v", code, out)
	}
	if code, out = post(t, h, "/api/v/"+tok+"/answer", body(`,"gen":1,"answer":[1,2,3]`)); code != 200 || out["status"] != "passed" {
		t.Fatalf("answer: %d %v", code, out)
	}
	if code, _ = post(t, h, "/api/v/"+tok+"/open", `{"init_data":"user=%7B%22id%22%3A42%7D&hash=00"}`); code != 403 {
		t.Fatalf("forged init data: %d", code)
	}
	if code, _ = post(t, h, "/api/v/short/open", body("")); code != 404 {
		t.Fatalf("bad token: %d", code)
	}
	eng.err = gate.ErrForbidden
	if code, _ = post(t, h, "/api/v/"+tok+"/open", body("")); code != 403 {
		t.Fatalf("forbidden: %d", code)
	}
}

func TestRateLimitPerToken(t *testing.T) {
	h := Handler(Config{BotToken: token, Engine: &fakeEngine{}, EnableApp: true})
	tok := "abcdefghijklmnopqrstuv"
	b, _ := json.Marshal(SignInitData(token, 1, time.Now(), nil))
	var last int
	for i := 0; i < 45; i++ {
		last, _ = post(t, h, "/api/v/"+tok+"/open", `{"init_data":`+string(b)+`}`)
	}
	if last != 429 {
		t.Fatalf("no rate limit: %d", last)
	}
	// Another token is unaffected.
	if code, _ := post(t, h, "/api/v/zyxwvutsrqponmlkjihgfe/open", `{"init_data":`+string(b)+`}`); code != 200 {
		t.Fatalf("other token limited: %d", code)
	}
}

func TestInvalidRequestsCannotExhaustUserQuota(t *testing.T) {
	h := Handler(Config{BotToken: token, Engine: &fakeEngine{}, EnableApp: true})
	path := "/api/v/abcdefghijklmnopqrstuv/open"
	for i := 0; i < 60; i++ {
		post(t, h, path, `{"init_data":"forged"}`)
	}
	other, _ := json.Marshal(SignInitData(token, 99, time.Now(), nil))
	for i := 0; i < 60; i++ {
		post(t, h, path, `{"init_data":`+string(other)+`}`)
	}
	own, _ := json.Marshal(SignInitData(token, 42, time.Now(), nil))
	if code, _ := post(t, h, path, `{"init_data":`+string(own)+`}`); code != 200 {
		t.Fatalf("other clients exhausted authenticated user's quota: %d", code)
	}
}

func TestPendingGateIsRetryable(t *testing.T) {
	h := Handler(Config{BotToken: token, Engine: &fakeEngine{err: gate.ErrPending}, EnableApp: true})
	init, _ := json.Marshal(SignInitData(token, 42, time.Now(), nil))
	code, body := post(t, h, "/api/v/abcdefghijklmnopqrstuv/open", `{"init_data":`+string(init)+`}`)
	if code != http.StatusServiceUnavailable || body["error"] != "pending" {
		t.Fatalf("pending response = %d %v", code, body)
	}
}

func TestPagesAndHealth(t *testing.T) {
	h := Handler(Config{BotToken: token, Engine: &fakeEngine{}, EnableApp: true, Version: "v9",
		Health: func(context.Context) error { return nil }})
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	if r := get("/healthz"); r.Code != 200 || !strings.Contains(r.Body.String(), "v9") {
		t.Fatalf("healthz: %d %s", r.Code, r.Body)
	}
	r := get("/v/abcdefghijklmnopqrstuv")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "/assets/app.js") || r.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("page: %d", r.Code)
	}
	if r := get("/assets/app.js"); r.Code != 200 || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("asset: %d", r.Code)
	}
	if r := get("/assets/embed.go"); r.Code != 404 {
		t.Fatalf("source file served: %d", r.Code)
	}
	// Without a public URL only /healthz exists.
	h2 := Handler(Config{BotToken: token, Engine: &fakeEngine{}})
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v/abcdefghijklmnopqrstuv", nil))
	if rec.Code != 404 {
		t.Fatalf("mini app served while disabled: %d", rec.Code)
	}
	_, _ = io.Discard.Write(nil)
}
