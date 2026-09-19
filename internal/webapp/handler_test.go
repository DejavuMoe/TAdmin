package webapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"tadmin/internal/store"
	"tadmin/internal/verify"
)

const webTestBotToken = "987654321:synthetic-web-token-for-tests"

func TestVerificationPageAndAPIHeadersDoNotLeakServerState(t *testing.T) {
	handler, _, token, initData := newTestHandler(t)

	page := performRequest(handler, http.MethodGet, "/verify/"+token, "", "")
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d, body = %s", page.Code, page.Body.String())
	}
	if contentType := page.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Errorf("page Content-Type = %q", contentType)
	}
	assertSecurityHeaders(t, page)
	if strings.Contains(page.Body.String(), token) {
		t.Fatal("static page unexpectedly embeds the session token")
	}

	body := `{"init_data":` + mustJSON(t, initData) + `}`
	opened := performRequest(handler, http.MethodPost, "/api/verify/"+token+"/open", "application/json", body)
	if opened.Code != http.StatusOK {
		t.Fatalf("open status = %d, body = %s", opened.Code, opened.Body.String())
	}
	assertSecurityHeaders(t, opened)
	for _, secret := range []string{webTestBotToken, initData, "challenge_secret", "user_id", `"targets"`, `"cells"`, `"correct"`} {
		if strings.Contains(opened.Body.String(), secret) {
			t.Errorf("open response leaks %q", secret)
		}
	}
	var view apiView
	if err := json.Unmarshal(opened.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode open response: %v", err)
	}
	if view.Status != store.VerificationChallengeIssued || view.Generation != 1 || view.Prompt == "" || view.ImageURL == "" || view.RemainingAttempts != verify.MaxAttempts {
		t.Fatalf("open response = %+v", view)
	}

	image := performRequest(handler, http.MethodGet, view.ImageURL, "", "")
	if image.Code != http.StatusOK {
		t.Fatalf("image status = %d, body = %s", image.Code, image.Body.String())
	}
	if image.Header().Get("Content-Type") != "image/png" {
		t.Errorf("image Content-Type = %q", image.Header().Get("Content-Type"))
	}
	assertSecurityHeaders(t, image)

	wrongAnswer := `{"init_data":` + mustJSON(t, initData) + `,"generation":1,"clicks":[{"x":0.1,"y":0.1},{"x":0.1,"y":0.1}]}`
	answered := performRequest(handler, http.MethodPost, "/api/verify/"+token+"/answer", "application/json", wrongAnswer)
	if answered.Code != http.StatusOK {
		t.Fatalf("answer status = %d, body = %s", answered.Code, answered.Body.String())
	}
	var retry apiView
	if err := json.Unmarshal(answered.Body.Bytes(), &retry); err != nil {
		t.Fatal(err)
	}
	if retry.Generation != 2 || retry.RemainingAttempts != 2 {
		t.Fatalf("retry response = %+v", retry)
	}
	oldImage := performRequest(handler, http.MethodGet, view.ImageURL, "", "")
	if oldImage.Code != http.StatusConflict || !strings.Contains(oldImage.Body.String(), "stale_challenge") {
		t.Fatalf("old image status = %d, body = %s", oldImage.Code, oldImage.Body.String())
	}
}

func TestVerificationHTTPBoundaries(t *testing.T) {
	handler, _, token, initData := newTestHandler(t)
	endpoint := "/api/verify/" + token + "/open"
	tests := []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
		want        int
	}{
		{name: "method", method: http.MethodGet, path: endpoint, want: http.StatusMethodNotAllowed},
		{name: "content type", method: http.MethodPost, path: endpoint, body: `{}`, want: http.StatusUnsupportedMediaType},
		{name: "unknown field", method: http.MethodPost, path: endpoint, contentType: "application/json", body: `{"init_data":"x","extra":true}`, want: http.StatusBadRequest},
		{name: "multiple values", method: http.MethodPost, path: endpoint, contentType: "application/json", body: `{} {}`, want: http.StatusBadRequest},
		{name: "query string", method: http.MethodPost, path: endpoint + "?init_data=forbidden", contentType: "application/json", body: `{}`, want: http.StatusBadRequest},
		{name: "large body", method: http.MethodPost, path: endpoint, contentType: "application/json", body: `{"init_data":"` + strings.Repeat("x", apiBodyLimit) + `"}`, want: http.StatusRequestEntityTooLarge},
		{name: "invalid number", method: http.MethodPost, path: "/api/verify/" + token + "/answer", contentType: "application/json", body: `{"init_data":"x","generation":1,"clicks":[{"x":NaN,"y":0},{"x":0,"y":0}]}`, want: http.StatusBadRequest},
		{name: "no create backdoor", method: http.MethodPost, path: "/api/verify/create", contentType: "application/json", body: `{}`, want: http.StatusNotFound},
		{name: "invalid token", method: http.MethodPost, path: "/api/verify/not-valid/open", contentType: "application/json", body: `{}`, want: http.StatusNotFound},
	}
	_ = initData
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(handler, test.method, test.path, test.contentType, test.body)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.want, response.Body.String())
			}
			assertSecurityHeaders(t, response)
			if test.name == "method" && response.Header().Get("Allow") != http.MethodPost {
				t.Errorf("Allow = %q", response.Header().Get("Allow"))
			}
		})
	}
}

func TestVerificationAuthorizationFailureIsGeneric(t *testing.T) {
	handler, _, token, _ := newTestHandler(t)
	marker := "raw-init-data-must-not-be-reflected"
	body := `{"init_data":` + mustJSON(t, marker) + `}`
	response := performRequest(handler, http.MethodPost, "/api/verify/"+token+"/open", "application/json", body)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), marker) || strings.Contains(response.Body.String(), webTestBotToken) {
		t.Fatal("authorization error reflected sensitive input")
	}
}

func TestFixedWindowLimiter(t *testing.T) {
	limiter := newFixedWindowLimiter(time.Minute, 2, 2)
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if !limiter.allow("session", now) || !limiter.allow("session", now) {
		t.Fatal("requests within limit were rejected")
	}
	if limiter.allow("session", now) {
		t.Fatal("request above limit was allowed")
	}
	if !limiter.allow("session", now.Add(time.Minute)) {
		t.Fatal("new window request was rejected")
	}
}

func TestVerificationAPIReturnsRateLimit(t *testing.T) {
	handler, _, token, initData := newTestHandler(t)
	handler.sessionLimiter = newFixedWindowLimiter(time.Minute, 1, 8)
	body := `{"init_data":` + mustJSON(t, initData) + `}`
	endpoint := "/api/verify/" + token + "/open"
	if first := performRequest(handler, http.MethodPost, endpoint, "application/json", body); first.Code != http.StatusOK {
		t.Fatalf("first status = %d", first.Code)
	}
	second := performRequest(handler, http.MethodPost, endpoint, "application/json", body)
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") != "60" {
		t.Fatalf("second status = %d, Retry-After = %q", second.Code, second.Header().Get("Retry-After"))
	}
}

func newTestHandler(t *testing.T) (*Handler, *store.Store, string, string) {
	t.Helper()
	ctx := t.Context()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "tadmin.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.SaveManagedChat(ctx, -10099, "Web Test", "en", true, now); err != nil {
		t.Fatalf("SaveManagedChat() error = %v", err)
	}
	service := verify.NewService(database, webTestBotToken)
	token, err := service.CreateSession(ctx, verify.CreateParams{
		ChatID: -10099, UserID: 8080, SourceKind: "join_request", SourceRef: "opaque", Language: "en",
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	initData := signedWebInitData(t, url.Values{
		"auth_date":   {strconv.FormatInt(now.Unix(), 10)},
		"start_param": {token},
		"user":        {`{"id":8080}`},
	})
	return New(service), database, token, initData
}

func signedWebInitData(t *testing.T, values url.Values) string {
	t.Helper()
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values.Get(key))
	}
	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMAC.Write([]byte(webTestBotToken))
	checkMAC := hmac.New(sha256.New, secretMAC.Sum(nil))
	_, _ = checkMAC.Write([]byte(strings.Join(lines, "\n")))
	values.Set("hash", hex.EncodeToString(checkMAC.Sum(nil)))
	return values.Encode()
}

func performRequest(handler http.Handler, method, path, contentType, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertSecurityHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	for _, name := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy"} {
		if response.Header().Get(name) == "" {
			t.Errorf("missing %s header", name)
		}
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestImageCancellationWhileRendererBusy(t *testing.T) {
	handler, _, token, _ := newTestHandler(t)
	handler.imageSlots <- struct{}{}
	handler.imageSlots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/verify/"+token+"/image/1.png", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", response.Code)
	}
	<-handler.imageSlots
	<-handler.imageSlots
	if next := performRequest(handler, http.MethodGet, "/api/verify/"+token+"/image/1.png", "", ""); next.Code != http.StatusOK {
		t.Fatal("renderer did not recover")
	}
}
