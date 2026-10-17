package intel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func layaBody() map[string]any {
	return map[string]any{
		"model": "laya-rl-agent", "answers": answers(allSignals(0.99)),
		"routing": map[string]any{"model": "multilingual", "repo": LayaModel},
		"usage":   map[string]any{"truncated": false, "state_tokens_dropped": 0, "truncated_questions": []string{}},
	}
}

func TestLayaContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"complete", func(b map[string]any) {}},
		{"strict response", func(b map[string]any) { delete(b, "routing") }},
		{"no truncation report", func(b map[string]any) { delete(b, "usage") }},
		{"truncated", func(b map[string]any) { b["usage"].(map[string]any)["truncated"] = true }},
		{"dropped tokens", func(b map[string]any) { b["usage"].(map[string]any)["state_tokens_dropped"] = 3 }},
		{"truncated question", func(b map[string]any) { b["usage"].(map[string]any)["truncated_questions"] = []string{"scam"} }},
		{"wrong checkpoint", func(b map[string]any) { b["routing"].(map[string]any)["model"] = "english" }},
		{"missing answer", func(b map[string]any) { delete(b["answers"].(map[string]any), SignalScam) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := layaBody()
			tc.mutate(body)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request systemOneRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if r.URL.Path != systemOnePath || r.Header.Get("Authorization") != "Bearer "+testKey || request.Model != LayaModel || len(request.Questions) != 5 || request.MaxLen != 2048 || request.HeadMaxLen != 1024 {
					t.Error("unexpected Laya request")
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer srv.Close()
			a, err := NewTypeSafe(Options{Provider: "laya", BaseURL: srv.URL, APIKey: testKey})
			if err != nil {
				t.Fatal(err)
			}
			got, err := a.Assess(context.Background(), spamProfile())
			if tc.name != "complete" {
				if !errors.Is(err, ErrMalformedResponse) {
					t.Fatalf("expected rejected response, got %v", err)
				}
				return
			}
			if err != nil || !got.HintOnly || got.Model != LayaModel || got.Risk != 0.99 {
				t.Fatalf("assessment=%+v err=%v", got, err)
			}
		})
	}
}

func TestLayaOptions(t *testing.T) {
	for _, opts := range []Options{
		{Provider: "unknown"},
		{Provider: "laya"},
		{Provider: "laya", BaseURL: "http://laya:8000", Model: "jev-latest"},
		{BaseURL: "http://user:secret@localhost"},
		{BaseURL: "https://example.com?key=secret"},
	} {
		opts.APIKey = testKey
		if _, err := NewTypeSafe(opts); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}

// TestLayaLive is opt-in: it uses synthetic profiles, never a bot or a database.
func TestLayaLive(t *testing.T) {
	base := os.Getenv("TADMIN_LAYA_TEST_URL")
	if base == "" {
		t.Skip("set TADMIN_LAYA_TEST_URL and TADMIN_LAYA_TEST_KEY for real inference")
	}
	a, err := NewTypeSafe(Options{Provider: "laya", BaseURL: base, APIKey: os.Getenv("TADMIN_LAYA_TEST_KEY")})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []Profile{
		{FirstName: "李明", Bio: "喜欢 Go 编程和摄影", ChatTitle: "Go 语言交流"},
		spamProfile(),
		{FirstName: "Alex", Bio: "Backend developer", ChatTitle: "Programming"},
	} {
		got, err := a.Assess(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		if !got.HintOnly || got.Model == "" || len(got.Signals) != 5 {
			t.Fatalf("incomplete result: %+v", got)
		}
		t.Logf("model=%s latency=%s risk=%g signals=%v (not an accuracy assertion)", got.Model, got.Latency, got.Risk, got.Signals)
	}
}
