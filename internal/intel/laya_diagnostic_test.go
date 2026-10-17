package intel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Export the actual adapter's synthetic wire requests for independent replay.
func TestLayaExportRequests(t *testing.T) {
	path := os.Getenv("TADMIN_LAYA_EXPORT")
	if path == "" {
		t.Skip("set TADMIN_LAYA_EXPORT to write synthetic diagnostic requests")
	}
	requests := map[string]json.RawMessage{}
	captured := make(chan json.RawMessage, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		captured <- body
		_ = json.NewEncoder(w).Encode(layaBody())
	}))
	defer srv.Close()
	a, err := NewTypeSafe(Options{Provider: "laya", BaseURL: srv.URL, APIKey: "synthetic-export-key"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		profile Profile
	}{
		{"normal_zh", Profile{FirstName: "李明", Bio: "喜欢 Go 编程和摄影", ChatTitle: "Go 语言交流"}},
		{"normal_en", Profile{FirstName: "Alex", Bio: "Backend developer", ChatTitle: "Programming"}},
		{"advert_zh", spamProfile()},
	} {
		if _, err := a.Assess(context.Background(), tc.profile); err != nil {
			t.Fatal(err)
		}
		requests[tc.name] = <-captured
	}
	b, err := json.MarshalIndent(requests, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
