package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"tadmin/assets/migrations"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSingleInstanceLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	a, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := Open(context.Background(), path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second open = %v, want ErrLocked", err)
	}
	if err := a.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Backup(context.Background(), filepath.Join(t.TempDir(), "b.db")); err != nil {
		t.Fatal(err)
	}
}

func TestChatSettingsDefaultsAndUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.UpsertChat(ctx, &Chat{ID: -100, Title: "G", Status: ChatActive}); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetChat(ctx, -100)
	if err != nil || c.Settings.Verify.Timeout != 180 || !c.Settings.Verify.Enabled {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	if _, err := s.UpdateSettings(ctx, -100, func(st *Settings) { st.Verify.Kind = "math" }); err != nil {
		t.Fatal(err)
	}
	// Re-upserting identity must keep settings.
	if err := s.UpsertChat(ctx, &Chat{ID: -100, Title: "G2", Status: ChatActive}); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetChat(ctx, -100)
	if c.Settings.Verify.Kind != "math" || c.Title != "G2" {
		t.Fatalf("settings lost: %+v", c.Settings.Verify)
	}
}

func TestGateUniqueOpenAndDue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	s.SetClock(func() time.Time { return now })
	g := &Gate{ChatID: -1, UserID: 7, Source: SourceJoin, State: StateNew, Delivery: DeliveryOverlay, Kind: "image",
		Seed: []byte("0123456789abcdef"), Gen: 1, MaxAttempts: 3, Deadline: now.Add(time.Minute), NextAt: now, Token: "t1"}
	created, _, err := s.CreateGate(ctx, g)
	if err != nil || !created || g.ID == 0 {
		t.Fatalf("create: %v %v", created, err)
	}
	dup := *g
	dup.Token = "t2"
	created, ex, err := s.CreateGate(ctx, &dup)
	if err != nil || created || ex.ID != g.ID {
		t.Fatalf("dup create: %v %+v %v", created, ex, err)
	}
	ids, _ := s.DueGates(ctx, now, 10)
	if len(ids) != 1 {
		t.Fatalf("due = %v", ids)
	}
	g.State, g.Outcome = StateClosed, OutcomeTimeout
	if err := s.SaveGate(ctx, g); err != nil {
		t.Fatal(err)
	}
	n, _ := s.RecentFailures(ctx, -1, 7, now.Add(-time.Hour))
	if n != 1 {
		t.Fatalf("failures = %d", n)
	}
	// A new gate may be opened after the old one closed.
	if created, _, err := s.CreateGate(ctx, &dup); err != nil || !created {
		t.Fatalf("reopen: %v %v", created, err)
	}
	got, err := s.GateByToken(ctx, "t2")
	if err != nil || got.UserID != 7 {
		t.Fatalf("by token: %+v %v", got, err)
	}
}

func TestGateSourceFallbackPersists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	g := &Gate{ChatID: -1, UserID: 7, Source: SourceGuard, State: StateNew, Token: "guard-fallback", Seed: []byte("seed")}
	if _, _, err := s.CreateGate(ctx, g); err != nil {
		t.Fatal(err)
	}
	g.Source = SourceRequest
	if err := s.SaveGate(ctx, g); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetGate(ctx, g.ID)
	if err != nil || got.Source != SourceRequest {
		t.Fatalf("fallback source lost: %+v, %v", got, err)
	}
}

func TestStatsAcrossDST(t *testing.T) {
	s := newTestStore(t)
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, loc)
	s.SetClock(func() time.Time { return now })
	ctx := context.Background()
	if err := s.AddEvent(ctx, Event{ChatID: -1, Kind: EvJoin}); err != nil {
		t.Fatal(err)
	}
	days, err := s.Stats(ctx, -1, 3, loc)
	if err != nil || days[2].Counts[EvJoin] != 1 || days[1].Counts[EvJoin] != 0 {
		t.Fatalf("event assigned to wrong calendar date: %+v, %v", days, err)
	}
}

func TestDeletedSourceCannotEnqueue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	src, _, err := s.AddSource(ctx, "https://example.com/feed", "rss", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ObserveURLs(ctx, src.ID, []Observed{{URL: "https://example.com/late"}}, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted source accepted observations: %v", err)
	}
}

func TestDeletedSourceQueueDoesNotAttachToNewSource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, _, _ := s.AddSource(ctx, "https://example.com/first", "rss", "")
	if err := s.EnqueueURL(ctx, "https://example.com/queued", a.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSource(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	b, _, err := s.AddSource(ctx, "https://example.com/second", "rss", "")
	if err != nil {
		t.Fatal(err)
	}
	if b.ID == a.ID {
		t.Fatal("deleted id reused by a new source")
	}
	if _, err := s.ObserveURLs(ctx, a.ID, []Observed{{URL: "https://example.com/late"}}, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old fetch wrote into replacement source: %v", err)
	}
	u, err := s.NextQueued(ctx, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if u.SourceID == b.ID {
		t.Fatal("retained queue attached to an unrelated source")
	}
}

func TestSourceIDMigrationPreservesData(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	body, err := migrations.FS.ReadFile("0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range splitSQL(string(body)) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL); INSERT INTO schema_migrations VALUES ('0001_init.sql', 1)`); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.AddSource(ctx, "https://example.com/kept", "rss", "kept")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueURL(ctx, "https://example.com/page", a.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSource(ctx, a.ID)
	if err != nil || got.URL != a.URL || got.Title != a.Title {
		t.Fatalf("source lost: %+v %v", got, err)
	}
	if err := s.DeleteSource(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	b, _, err := s.AddSource(ctx, "https://example.com/new", "rss", "")
	if err != nil || b.ID <= a.ID {
		t.Fatalf("sequence not preserved: %+v %v", b, err)
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestChatMigrationConflictRollsBack(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []int64{-1, -2} {
		if err := s.UpsertChat(ctx, &Chat{ID: id, Title: "original", Status: ChatActive}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.CreateGate(ctx, &Gate{ChatID: id, UserID: 7, Source: SourceJoin, State: StateNew, Token: fmt.Sprint(id), Seed: []byte("seed")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MigrateChat(ctx, -1, -2); err == nil {
		t.Fatal("migration silently left gates on the retired chat")
	}
	if _, err := s.GetChat(ctx, -1); err != nil {
		t.Fatal("failed migration lost source chat")
	}
}

func TestPreviewObserveAndQueue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	s.SetClock(func() time.Time { return now })
	src, created, err := s.AddSource(ctx, "https://e.example/sitemap.xml", "sitemap", "")
	if err != nil || !created {
		t.Fatal(err)
	}
	t1 := now.Add(-48 * time.Hour)
	r, err := s.ObserveURLs(ctx, src.ID, []Observed{{URL: "https://e.example/a", Updated: t1}, {URL: "https://e.example/b", Updated: t1}}, false)
	if err != nil || r.New != 2 || r.Queued != 0 {
		t.Fatalf("baseline: %+v %v", r, err)
	}
	r, _ = s.ObserveURLs(ctx, src.ID, []Observed{{URL: "https://e.example/a", Updated: now.Add(-time.Hour)}, {URL: "https://e.example/c"}}, true)
	if r.Changed != 1 || r.New != 1 || r.Queued != 2 {
		t.Fatalf("changes: %+v", r)
	}
	if err := s.EnqueueURL(ctx, "https://x.example/manual", 0, 10); err != nil {
		t.Fatal(err)
	}
	u, err := s.NextQueued(ctx, now)
	if err != nil || u.URL != "https://x.example/manual" {
		t.Fatalf("priority: %+v %v", u, err)
	}
	if err := s.FinishURL(ctx, u.URL, URLDone, "updated", "ok"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.PreviewStats(ctx, now.Add(-time.Hour))
	if st.Queued != 2 || st.DoneSince != 1 || st.Tracked != 4 {
		t.Fatalf("stats: %+v", st)
	}
}

func TestStatsAndDeletions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		_ = s.AddEvent(ctx, Event{ChatID: -1, Kind: EvJoin})
	}
	_ = s.AddEvent(ctx, Event{ChatID: -1, Kind: EvPass, Value: 4200})
	_ = s.AddEvent(ctx, Event{At: now.AddDate(0, 0, -1), ChatID: -1, Kind: EvFail})
	days, err := s.Stats(ctx, -1, 7, time.UTC)
	if err != nil || len(days) != 7 || days[6].Counts[EvJoin] != 3 || days[5].Counts[EvFail] != 1 || len(days[6].SolveMS) != 1 {
		t.Fatalf("stats: %+v %v", days, err)
	}
	_ = s.ScheduleDeletion(ctx, Deletion{ChatID: -1, MessageID: 5, Due: now.Add(-time.Second)})
	_ = s.ScheduleDeletion(ctx, Deletion{ChatID: -1, MessageID: 6, Due: now.Add(time.Hour)})
	due, _ := s.DueDeletions(ctx, now, 10)
	if len(due) != 1 || due[0].MessageID != 5 {
		t.Fatalf("due deletions: %+v", due)
	}
}
