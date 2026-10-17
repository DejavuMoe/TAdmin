package gate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tadmin/internal/intel"
	"tadmin/internal/store"
	"tadmin/internal/tg"
	"tadmin/internal/tgtest"
)

type ablationProfile struct {
	ID, Lang, Name, Bio, Group, Username string
	LastName                             string `json:"last_name"`
}

type ablationCase struct {
	Name, Source, Event, Fault, CAS string
	Profile                         ablationProfile
	Response                        json.RawMessage
	Scores                          map[string]float64
}

type ablationRun struct {
	Mode, State, Outcome, Reason     string
	Risk                             float64
	Flags, Attempts                  int
	Actor                            int64
	JevCalls, ProfileReads, CASCalls int
	ReviewHints                      int
	ReadyAfterMS, AnswerWindowMS     int64
	PreparationWallMS                int64
	Actions                          []tgtest.Call
}

type ablationCAS func(context.Context, int64) (bool, error)

func (f ablationCAS) Listed(ctx context.Context, id int64) (bool, error) { return f(ctx, id) }

func ablationRead(t *testing.T, path, hash string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../..", path))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(b)); hash != "" && got != hash {
		t.Fatalf("frozen input changed: %s = %s", path, got)
	}
	return b
}

func runJevAblation(t *testing.T, tc ablationCase, mode string, questions any) ablationRun {
	t.Helper()
	e := newEnv(t, func(s *store.Settings) {
		s.Verify.AI, s.Verify.Kind = mode, "math"
		s.Verify.CAS = tc.CAS != ""
	})
	e.eng.cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	e.eng.log = e.eng.cfg.Logger
	p := tc.Profile
	if p.Group == "" {
		p.Group = "Go programming"
		if p.Lang == "zh" {
			p.Group = "Go 编程交流"
		}
	}
	e.chat.Title = p.Group
	if err := e.st.UpsertChat(e.ctx, e.chat); err != nil {
		t.Fatal(err)
	}
	u := tg.User{ID: userID, FirstName: p.Name, LastName: p.LastName, Username: p.Username, LanguageCode: p.Lang}
	e.srv.SetChat(tg.ChatFullInfo{ID: userID, Type: tg.ChatPrivate, FirstName: p.Name, LastName: p.LastName, Bio: p.Bio})
	var requests atomic.Int32
	casCalls := 0
	e.eng.cfg.CAS = ablationCAS(func(context.Context, int64) (bool, error) {
		casCalls++
		if tc.CAS == "error" {
			return false, errors.New("synthetic CAS failure")
		}
		return tc.CAS == "hit", nil
	})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body struct {
			Model     string
			State     map[string]map[string]string
			Questions any
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		wantState := map[string]map[string]string{
			"joining_account": {"first_name": p.Name, "last_name": p.LastName, "username": p.Username, "bio": p.Bio},
			"group":           {"title": p.Group, "description": ""},
		}
		if r.Method != "POST" || r.URL.Path != "/v1/systemone" || body.Model != "typesafe/jev-latest" || !reflect.DeepEqual(body.State, wantState) || !reflect.DeepEqual(body.Questions, questions) {
			t.Error("runtime request differs from frozen model/state/questions")
		}
		if len(e.srv.Calls("sendPhoto")) != 0 {
			t.Error("challenge was delivered before checks finished")
		}
		if tc.Source == store.SourceGuard && len(e.srv.Calls("sendChatJoinRequestWebApp")) != 1 {
			t.Error("Guard launch must precede optional checks")
		}
		switch tc.Fault {
		case "http503":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "malformed":
			_, _ = io.WriteString(w, `{"answers":{}}`)
		case "timeout":
			<-r.Context().Done()
		default:
			if tc.Fault == "slow" {
				e.advance(2 * time.Second)
			}
			_, _ = w.Write(tc.Response)
		}
	}))
	t.Cleanup(api.Close)
	if tc.Fault != "unconfigured" {
		a, err := intel.NewTypeSafe(intel.Options{BaseURL: api.URL, APIKey: "test-only", Model: "typesafe/jev-latest"})
		if err != nil {
			t.Fatal(err)
		}
		e.eng.cfg.Assessor = a
	}
	start, wall := e.clock(), time.Now()
	if tc.Source == store.SourceJoin {
		e.srv.SetMember(chatID, tg.ChatMember{Status: tg.StatusMember, User: u})
		e.eng.OnMemberUpdate(e.ctx, e.chat, &tg.ChatMemberUpdated{From: u,
			OldChatMember: tg.ChatMember{Status: tg.StatusLeft, User: u}, NewChatMember: tg.ChatMember{Status: tg.StatusMember, User: u}})
	} else {
		query := ""
		if tc.Source == store.SourceGuard {
			e.eng.cfg.WebURL, query = "https://verify.example", "fixed-query"
		}
		e.eng.OnJoinRequest(e.ctx, e.chat, &tg.ChatJoinRequest{From: u, UserChatID: userID, QueryID: query, Date: start.Unix()})
	}
	g := e.gate(userID)
	g.Seed = bytes.Repeat([]byte{7}, 32)
	if err := e.st.SaveGate(e.ctx, g); err != nil {
		t.Fatal(err)
	}
	e.tick()
	g = e.gate(userID)
	run := ablationRun{Mode: mode, ReadyAfterMS: e.clock().Sub(start).Milliseconds(), AnswerWindowMS: g.Deadline.Sub(e.clock()).Milliseconds(), PreparationWallMS: time.Since(wall).Milliseconds()}
	if g.Source != tc.Source || g.Deadline != start.Add(180*time.Second) {
		t.Fatalf("entry/deadline changed: %s %v", g.Source, g.Deadline)
	}
	if tc.Fault == "restart" {
		e.eng = New(e.eng.cfg)
		e.tick()
	}
	callback := func(action string) {
		e.eng.OnCallback(e.ctx, &tg.CallbackQuery{ID: "fixed-callback", From: u, Data: "g:" + itoa(g.ID) + ":" + action})
		g = e.gate(userID)
	}
	answer := func(ans []int) {
		if tc.Source == store.SourceGuard {
			if _, err := e.eng.WebAnswer(e.ctx, g.Token, userID, g.QueryID, g.Gen, ans); err != nil {
				t.Fatal(err)
			}
			g = e.gate(userID)
		} else {
			callback(fmt.Sprintf("o:%d:%d", g.Gen, ans[0]))
		}
	}
	wantOutcome := store.OutcomePass
	if tc.CAS == "hit" {
		wantOutcome = store.OutcomeCAS
	} else {
		if g.State != store.StateChallenge || g.Has(store.FlagHighRisk) {
			t.Fatalf("hint changed verification: %s flags=%d", g.State, g.Flags)
		}
		e.advance(start.Add(10 * time.Second).Sub(e.clock()))
		switch tc.Event {
		case "wrong":
			wantOutcome = store.OutcomeFail
			for i := 0; i < 3; i++ {
				answer(wrong(t, g))
			}
		case "timeout":
			wantOutcome = store.OutcomeTimeout
			e.advance(g.Deadline.Add(time.Second).Sub(e.clock()))
			e.tick()
		case "help":
			wantOutcome = store.OutcomeApprove
			if tc.Source == store.SourceGuard {
				if _, err := e.eng.WebHelp(e.ctx, g.Token, userID, g.QueryID); err != nil {
					t.Fatal(err)
				}
				g = e.gate(userID)
			} else {
				callback("h")
			}
			if g.State != store.StateReview {
				t.Fatalf("help did not enter review: %s", g.State)
			}
			e.advance(time.Second)
			e.eng.OnCallback(e.ctx, &tg.CallbackQuery{ID: "admin", From: user(ownerID), Data: "g:" + itoa(g.ID) + ":a"})
		default:
			answer(solve(t, g))
		}
	}
	g = e.gate(userID)
	if g.State != store.StateClosed || g.Outcome != wantOutcome || g.Has(store.FlagHighRisk) {
		t.Fatalf("unexpected terminal state: %s/%s flags=%d", g.State, g.Outcome, g.Flags)
	}
	run.State, run.Outcome, run.Risk, run.Reason = g.State, g.Outcome, g.Risk, g.RiskReason
	run.Flags, run.Attempts, run.Actor = g.Flags, g.Attempts, g.ActorID
	run.JevCalls, run.CASCalls = int(requests.Load()), casCalls
	for _, c := range e.srv.Calls("getChat") {
		if c.Int("chat_id") == userID {
			run.ProfileReads++
		}
	}
	for _, c := range e.srv.Calls("sendMessage", "editMessageText") {
		if strings.Contains(c.Str("text"), "AI 资料风险") || strings.Contains(c.Str("text"), "AI profile risk") {
			run.ReviewHints++
		}
	}
	run.Actions = e.srv.Calls("restrictChatMember", "banChatMember", "unbanChatMember", "approveChatJoinRequest", "declineChatJoinRequest", "answerChatJoinRequestQuery")
	wantCalls, wantRisk, wantReason := 0, -1.0, ""
	if mode == "hint" && tc.Fault != "unconfigured" && tc.CAS != "hit" {
		wantCalls = 1
		if tc.Fault != "http503" && tc.Fault != "malformed" && tc.Fault != "timeout" {
			for _, signal := range intel.RiskSignals() {
				if tc.Scores[signal] > wantRisk {
					wantRisk, wantReason = tc.Scores[signal], signal
				}
			}
		}
	}
	wantCAS, wantHints, wantDelay := 0, 0, int64(0)
	if tc.CAS != "" {
		wantCAS = 1
	}
	if tc.Event == "help" && wantRisk >= 0 {
		wantHints = 1
	}
	if mode == "hint" && tc.Fault == "slow" {
		wantDelay = 2000
	}
	if run.JevCalls != wantCalls || run.ProfileReads != wantCalls || run.CASCalls != wantCAS || run.Risk != wantRisk || run.Reason != wantReason || run.ReviewHints != wantHints || run.ReadyAfterMS != wantDelay {
		t.Fatalf("unexpected assessment side effects: %+v", run)
	}
	return run
}

func TestJevHintAblation(t *testing.T) {
	const dataPath = "deploy/laya/eval/profiles-fresh96.jsonl"
	const responsePath = "docs/diagnostics/jev-2026-10-18-fresh96.json"
	data := ablationRead(t, dataPath, "f4cc2525d2adeb8cc2ed0cee96cd5fea58287705e68e9360ff059c4320aa19f0")
	responses := ablationRead(t, responsePath, "ff396c2d7ddc49c2e2b3244cc289ef8cfabc905a78dbdcce53e3f96fa9ca22f9")
	var baseline struct {
		Complete     bool
		Questions    any
		Observations []struct {
			ID       string
			Scores   map[string]float64
			Response json.RawMessage
		}
	}
	if err := json.Unmarshal(responses, &baseline); err != nil || !baseline.Complete || len(baseline.Observations) != 96 {
		t.Fatalf("incomplete baseline: %v", err)
	}
	sources := []string{store.SourceJoin, store.SourceRequest, store.SourceGuard}
	var cases []ablationCase
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var p ablationProfile
		if err := json.Unmarshal(scanner.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		for _, o := range baseline.Observations {
			if o.ID == p.ID {
				for _, source := range sources {
					cases = append(cases, ablationCase{Name: p.ID, Profile: p, Source: source, Event: "pass", Response: o.Response, Scores: o.Scores})
				}
			}
		}
	}
	if scanner.Err() != nil || len(cases) != 288 {
		t.Fatal("missing profile/response pairs", scanner.Err())
	}
	highScores, answers := map[string]float64{}, map[string]any{}
	for _, signal := range append(intel.RiskSignals(), intel.SignalAdFormatting) {
		highScores[signal] = 0.99
		answers[signal] = map[string]any{"type": "noul", "noul": 0.99}
	}
	high, err := json.Marshal(map[string]any{"model": "jev-1.13.0", "answers": answers})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		for _, name := range []string{"wrong", "timeout", "help", "http503", "malformed", "http_timeout", "unconfigured", "cas_clear", "cas_error", "cas_hit", "slow", "restart"} {
			tc := ablationCase{Name: name, Source: source, Event: "pass", Profile: ablationProfile{Name: "合成风险资料", Bio: "固定响应，仅测试流程", Lang: "zh"}, Response: high, Scores: highScores}
			switch {
			case name == "wrong" || name == "timeout" || name == "help":
				tc.Event = name
			case strings.HasPrefix(name, "cas_"):
				tc.CAS = strings.TrimPrefix(name, "cas_")
			case name == "http_timeout":
				tc.Fault = "timeout"
			default:
				tc.Fault = name
			}
			cases = append(cases, tc)
		}
	}
	type pair struct {
		Case, Source, Event string
		Off, Hint           ablationRun
	}
	report := struct {
		Complete    bool
		Runtime     string
		StartedAt   time.Time
		InputHashes map[string]string
		Pairs       []pair
	}{Runtime: runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version(), StartedAt: time.Now().UTC(), InputHashes: map[string]string{}}
	for _, path := range []string{dataPath, responsePath, "deploy/laya/eval/protocol-jev-hint-ablation.md", "internal/gate/jev_ablation_test.go", "internal/gate/steps.go", "internal/gate/intake.go", "internal/gate/interact.go", "internal/gate/deliver.go", "internal/gate/gate.go", "internal/intel/typesafe.go", "internal/intel/questions.go", "internal/intel/intel.go"} {
		report.InputHashes[path] = fmt.Sprintf("%x", sha256.Sum256(ablationRead(t, path, "")))
	}
	for _, tc := range cases {
		t.Run(tc.Name+"/"+tc.Source, func(t *testing.T) {
			p := pair{Case: tc.Name, Source: tc.Source, Event: tc.Event}
			t.Run("off", func(t *testing.T) { p.Off = runJevAblation(t, tc, "off", baseline.Questions) })
			t.Run("hint", func(t *testing.T) { p.Hint = runJevAblation(t, tc, "hint", baseline.Questions) })
			if !reflect.DeepEqual(p.Off.Actions, p.Hint.Actions) || p.Off.State != p.Hint.State || p.Off.Outcome != p.Hint.Outcome || p.Off.Flags != p.Hint.Flags || p.Off.Attempts != p.Hint.Attempts || p.Off.Actor != p.Hint.Actor {
				t.Errorf("hint changed admission behavior: off=%+v hint=%+v", p.Off, p.Hint)
			}
			report.Pairs = append(report.Pairs, p)
		})
	}
	for path, hash := range report.InputHashes {
		ablationRead(t, path, hash)
	}
	report.Complete = !t.Failed() && len(report.Pairs) == 324
	if path := os.Getenv("TADMIN_JEV_ABLATION_OUTPUT"); path != "" {
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := f.Write(append(b, '\n'))
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal(writeErr, closeErr)
		}
	}
	t.Logf("Jev hint/off: %d pairs, complete=%v; local replay only", len(report.Pairs), report.Complete)
}
