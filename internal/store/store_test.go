package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
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
