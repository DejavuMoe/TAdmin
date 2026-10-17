package gate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tadmin/internal/intel"
	"tadmin/internal/store"
	"tadmin/internal/tg"
	"tadmin/internal/tgtest"
)

type profileRecorder struct{ got intel.Profile }

func (r *profileRecorder) Assess(_ context.Context, p intel.Profile) (intel.Assessment, error) {
	r.got = p
	return intel.Assessment{}, nil
}

func TestJoinRequestBioSurvivesProfileLookupFailure(t *testing.T) {
	e := newEnv(t, func(s *store.Settings) { s.Verify.AI = "hint" })
	recorder := &profileRecorder{}
	e.eng.cfg.Assessor = recorder
	e.srv.Fail("getChat", tgtest.Fault{Code: 400, Description: "chat not found"})
	e.eng.OnJoinRequest(e.ctx, e.chat, &tg.ChatJoinRequest{Chat: tg.Chat{ID: chatID}, From: user(userID), UserChatID: userID, Bio: "Independent translator", Date: e.clock().Unix()})
	e.tick()
	if recorder.got.Bio != "Independent translator" {
		t.Fatalf("request bio lost: %+v", recorder.got)
	}
}

func TestAIAdmissionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, provider, mode string
		status               int
		broken, slow, review bool
	}{
		{"jev review", "jev", "review", 200, false, false, true},
		{"jev hint", "jev", "hint", 200, false, false, false},
		{"laya hint", "laya", "hint", 200, false, false, false},
		{"laya caps review", "laya", "review", 200, false, false, false},
		{"laya off", "laya", "off", 200, false, false, false},
		{"unauthorized", "laya", "review", 401, false, false, false},
		{"overloaded", "laya", "review", 503, false, false, false},
		{"malformed", "laya", "review", 200, true, false, false},
		{"timeout", "laya", "review", 200, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if tc.mode == "off" {
					t.Error("AI off made a request")
				}
				if tc.slow {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(tc.status)
				if tc.broken {
					_, _ = w.Write([]byte(`{"answers":{}}`))
					return
				}
				answers := map[string]any{}
				for _, id := range append(intel.RiskSignals(), intel.SignalAdFormatting) {
					answers[id] = map[string]any{"type": "noul", "noul": 0.99}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"answers": answers, "model": "test",
					"routing": map[string]string{"model": "multilingual", "repo": intel.LayaModel},
					"usage":   map[string]bool{"truncated": false},
				})
			}))
			defer srv.Close()
			e := newEnv(t, func(s *store.Settings) { s.Verify.AI = tc.mode; s.Verify.Kind = "math" })
			a, err := intel.NewTypeSafe(intel.Options{Provider: tc.provider, BaseURL: srv.URL, APIKey: "test-key", Timeout: 100 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			e.eng.cfg.Assessor = a
			e.join(userID, 0)
			g := e.gate(userID)
			if g.State != store.StateChallenge || g.Has(store.FlagHighRisk) != tc.review {
				t.Fatalf("state=%s flags=%v", g.State, g.Flags)
			}
			if tc.status == 200 && !tc.broken && !tc.slow && tc.mode != "off" && g.Risk != 0.99 {
				t.Fatalf("hint not persisted: risk=%v", g.Risk)
			}
			if len(e.srv.Calls("banChatMember")) != 0 {
				t.Fatal("AI banned a member")
			}
			e.answerViaButtons(g, solve(t, g), ephMsg(g))
			g = e.gate(userID)
			if tc.review {
				if g.State != store.StateReview {
					t.Fatalf("Jev review lost: %s", g.State)
				}
			} else if g.State != store.StateClosed || g.Outcome != store.OutcomePass {
				t.Fatalf("normal verification changed: %s/%s", g.State, g.Outcome)
			}
		})
	}
}
