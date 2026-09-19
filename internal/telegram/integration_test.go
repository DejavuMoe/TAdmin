package telegram

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tadmin/internal/config"
	"tadmin/internal/store"
	"tadmin/internal/verify"
	"tadmin/internal/webapp"
)

func TestPollingToHTTPSChallengeAndRestart(t *testing.T) {
	const token = "123:offline-integration-secret"
	var mu sync.Mutex
	member := "member"
	restrictions, restores := 0, 0
	now := time.Now().Unix()
	updates := []json.RawMessage{
		json.RawMessage(`{"update_id":1,"message":{"message_id":1,"date":1,"chat":{"id":1,"type":"private"},"from":{"id":1},"text":"/tadmin setup -1001","entities":[{"type":"bot_command","offset":0,"length":7}]}}`),
		json.RawMessage(fmt.Sprintf(`{"update_id":2,"chat_member":{"chat":{"id":-1001,"type":"supergroup","title":"Source"},"from":{"id":42},"date":%d,"old_chat_member":{"status":"left","user":{"id":42}},"new_chat_member":{"status":"member","user":{"id":42}}}}`, now)),
		json.RawMessage(`{"update_id":3,"message":{"message_id":3,"chat":{"id":-1001,"type":"supergroup"},"from":{"id":42},"text":"ordinary post-admission content https://example.test"}}`),
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var params map[string]json.RawMessage
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
				t.Error(err)
			}
		} else {
			_ = r.ParseMultipartForm(1 << 20)
			params = map[string]json.RawMessage{}
			for key, values := range r.Form {
				params[key], _ = json.Marshal(values[0])
			}
		}
		str := func(k string) string { return strings.Trim(string(params[k]), `"`) }
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		result := any(true)
		switch method {
		case "getMe":
			result = map[string]any{"id": 123, "is_bot": true, "username": "OfflineBot", "first_name": "Offline"}
		case "getWebhookInfo":
			result = map[string]any{"url": "", "pending_update_count": 0}
		case "getUpdates":
			if !strings.Contains(string(params["allowed_updates"]), "chat_member") {
				t.Error("missing explicit member subscription")
			}
			offset, _ := strconv.Atoi(str("offset"))
			batch := []json.RawMessage{}
			for i, u := range updates {
				if i+1 >= offset {
					batch = append(batch, u)
				}
			}
			result = batch
			if len(batch) == 0 {
				mu.Unlock()
				select {
				case <-r.Context().Done():
				case <-time.After(10 * time.Millisecond):
				}
				mu.Lock()
			}
		case "getChat":
			id, _ := strconv.ParseInt(str("chat_id"), 10, 64)
			result = map[string]any{"id": id, "type": "supergroup", "title": "Source", "permissions": map[string]bool{"can_send_messages": true}}
		case "getChatMember":
			status := member
			id, _ := strconv.ParseInt(str("user_id"), 10, 64)
			if id == 123 {
				status = "administrator"
			}
			result = map[string]any{"status": status, "is_member": true, "user": map[string]any{"id": id, "is_bot": id == 123}, "can_restrict_members": true, "can_invite_users": true}
		case "restrictChatMember":
			raw := params["permissions"]
			if len(raw) > 0 && raw[0] == '"' {
				var s string
				_ = json.Unmarshal(raw, &s)
				raw = []byte(s)
			}
			var rights map[string]bool
			_ = json.Unmarshal(raw, &rights)
			if rights["can_send_messages"] {
				restores++
				member = "member"
			} else {
				restrictions++
				member = "restricted"
			}
		case "sendMessage", "editMessageText":
			if v := str("message_thread_id"); v != "" && v != "0" {
				t.Error("topic API used")
			}
			result = map[string]any{"message_id": 50, "date": now, "chat": map[string]any{"id": -2001, "type": "supergroup"}}
		case "answerCallbackQuery":
		default:
			t.Errorf("unexpected API method: %s", method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer api.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "integration.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	service := verify.NewService(db, token)
	cfg := config.Config{OwnerUserID: 1, ManagementChatID: -2001, PublicBaseURL: "https://verify.example.test", DefaultLanguage: "en", BotToken: token}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := newClient(ctx, cfg, db, service, logger, "test", time.Now(), api.URL, api.Client())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); client.Start(ctx) }()
	defer func() { cancel(); <-done; client.Close(); db.Close() }()
	var session store.VerificationSession
	await(t, func() bool {
		session, err = db.GetVerificationSessionBySource(ctx, -1001, "direct_join", "2")
		return err == nil
	})
	mu.Lock()
	if restrictions != 1 || restores != 0 {
		t.Errorf("before verification restrict=%d restore=%d", restrictions, restores)
	}
	mu.Unlock()
	web := httptest.NewTLSServer(webapp.New(service, client.NotifyVerificationTerminal))
	defer web.Close()
	challenge, err := verify.NewChallenge(session.ChallengeSecret, session.Generation)
	if err != nil {
		t.Fatal(err)
	}
	var points []verify.Point
	for a := 0; a < 9; a++ {
		for b := 0; b < 9; b++ {
			p := []verify.Point{{X: (float64(a%3) + .5) / 3, Y: (float64(a/3) + .5) / 3}, {X: (float64(b%3) + .5) / 3, Y: (float64(b/3) + .5) / 3}}
			if ok, _ := challenge.Check(p); ok {
				points = p
			}
		}
	}
	values := url.Values{"auth_date": {strconv.FormatInt(time.Now().Unix(), 10)}, "user": {`{"id":42}`}}
	body, _ := json.Marshal(map[string]any{"init_data": integrationInitData(values, token), "generation": 1, "clicks": points})
	resp, err := web.Client().Post(web.URL+"/api/verify/"+session.ID+"/answer", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	await(t, func() bool { mu.Lock(); defer mu.Unlock(); return restores == 1 })
	await(t, func() bool { offset, _ := db.PollOffset(ctx); return offset >= 4 })
	cancel()
	<-done
	client.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen the same file and replay the same server history. Offset + durable
	// join state must prevent a second restriction or restoration.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	db, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	service = verify.NewService(db, token)
	client, err = newClient(ctx, cfg, db, service, logger, "test", time.Now(), api.URL, api.Client())
	if err != nil {
		t.Fatal(err)
	}
	done = make(chan struct{})
	go func() { defer close(done); client.Start(ctx) }()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if restrictions != 1 || restores != 1 {
		t.Fatalf("replayed enforcement restrict=%d restore=%d", restrictions, restores)
	}
}

func await(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
func integrationInitData(values url.Values, token string) string {
	keys := []string{}
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := []string{}
	for _, k := range keys {
		lines = append(lines, k+"="+values.Get(k))
	}
	key := hmac.New(sha256.New, []byte("WebAppData"))
	key.Write([]byte(token))
	mac := hmac.New(sha256.New, key.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	values.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return values.Encode()
}

func TestStartupConsumesLeaveBeforeRecoveringExpiredAction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catchup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.SaveManagedChatSetup(ctx, store.ManagedChat{ChatID: -1001, Title: "Source", Enabled: true, Language: "en", VerificationTimeoutSeconds: 180}, now); err != nil {
		t.Fatal(err)
	}
	item, _, err := db.CreateJoinCase(ctx, store.JoinCase{ChatID: -1001, UserID: 42, Path: store.JoinPathDirect, SourceKey: "old", SourceRef: "old", DeadlineAt: now.Add(-time.Minute)}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ScheduleCaseAction(ctx, item.ID, "kick", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getMe":
			fmt.Fprint(w, `{"ok":true,"result":{"id":123,"is_bot":true,"username":"TestBot"}}`)
		case "getWebhookInfo":
			fmt.Fprint(w, `{"ok":true,"result":{"url":""}}`)
		case "getUpdates":
			var p struct {
				Offset  int64 `json:"offset"`
				Timeout int   `json:"timeout"`
			}
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p.Offset == 0 {
				if p.Timeout != 0 {
					t.Error("startup catchup must not long-poll")
				}
				time.Sleep(40 * time.Millisecond)
				fmt.Fprint(w, `{"ok":true,"result":[{"update_id":9,"chat_member":{"chat":{"id":-1001,"type":"supergroup"},"date":1,"old_chat_member":{"status":"restricted","is_member":true,"user":{"id":42}},"new_chat_member":{"status":"left","user":{"id":42}}}}]}`)
			} else {
				time.Sleep(5 * time.Millisecond)
				fmt.Fprint(w, `{"ok":true,"result":[]}`)
			}
		default:
			calls.Add(1)
			fmt.Fprint(w, `{"ok":false,"error_code":403,"description":"unexpected action before catchup"}`)
		}
	}))
	defer api.Close()
	cfg := config.Config{OwnerUserID: 1, BotToken: "123:offline", DefaultLanguage: "en"}
	client, err := newClient(ctx, cfg, db, verify.NewService(db, cfg.BotToken), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", now, api.URL, api.Client())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = client.Start(ctx) }()
	await(t, func() bool {
		got, err := db.GetJoinCase(ctx, item.ID)
		return err == nil && got.Status == store.JoinCancelled
	})
	cancel()
	<-done
	client.Close()
	if calls.Load() != 0 {
		t.Fatal("replayed an obsolete action before consuming retained membership changes")
	}
}
