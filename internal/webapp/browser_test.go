package webapp

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"tadmin/internal/verify"
	"testing"
	"time"
)

// Opt-in local harness only: no fixture creation route exists in the binary.
// TADMIN_UI_FIXTURE points to an output JSON file outside the checkout.
func TestBrowserFixture(t *testing.T) {
	output := os.Getenv("TADMIN_UI_FIXTURE")
	if output == "" {
		t.Skip("opt-in local browser fixture")
	}
	handler, db, _, _ := newTestHandler(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	fixtures := map[string]any{"baseURL": server.URL}
	for _, name := range []string{"success", "failure", "stale", "mobile"} {
		token, err := handler.service.CreateSession(t.Context(), verify.CreateParams{ChatID: -10099, UserID: 8080, SourceKind: "direct_join", SourceRef: name, Language: "zh-CN"})
		if err != nil {
			t.Fatal(err)
		}
		session, err := db.GetVerificationSession(t.Context(), token)
		if err != nil {
			t.Fatal(err)
		}
		challenge, err := verify.NewChallenge(session.ChallengeSecret, session.Generation)
		if err != nil {
			t.Fatal(err)
		}
		var targets []int
		for a := 0; a < 9; a++ {
			for b := 0; b < 9; b++ {
				ok, _ := challenge.Check([]verify.Point{{X: (float64(a%3) + .5) / 3, Y: (float64(a/3) + .5) / 3}, {X: (float64(b%3) + .5) / 3, Y: (float64(b/3) + .5) / 3}})
				if ok {
					targets = []int{a, b}
				}
			}
		}
		signed := signedWebInitData(t, url.Values{"auth_date": {strconv.FormatInt(time.Now().Unix(), 10)}, "user": {`{"id":8080}`}})
		fixtures[name] = map[string]any{"token": token, "initData": signed, "targets": targets}
	}
	data, _ := json.Marshal(fixtures)
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("Local browser fixture ready")
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("browser harness timed out")
		case <-ticker.C:
			if _, err := os.Stat(output + ".done"); err == nil {
				return
			}
		}
	}
}
