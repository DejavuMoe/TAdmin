package preview

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tadmin/internal/preview/feeds"
	"tadmin/internal/store"
	"tadmin/internal/userbot"
)

type fakeRefresher struct {
	mu    sync.Mutex
	urls  []string
	flood bool
}

func (f *fakeRefresher) Ready() bool { return true }

func (f *fakeRefresher) RefreshPreview(ctx context.Context, url string) (userbot.RefreshResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flood {
		f.flood = false
		return userbot.RefreshResult{}, &userbot.FloodWaitError{Wait: 30 * time.Second}
	}
	f.urls = append(f.urls, url)
	return userbot.RefreshResult{Outcome: userbot.OutcomeUpdated, Reply: "Link preview updated"}, nil
}

func (f *fakeRefresher) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.urls...)
}

func TestSitemapChangesAreRefreshed(t *testing.T) {
	var mu sync.Mutex
	lastmod := "2026-09-01"
	extra := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>%[1]s/a</loc><lastmod>%[2]s</lastmod></url><url><loc>%[1]s/b</loc><lastmod>2026-09-01</lastmod></url>%[3]s</urlset>`,
			"http://"+r.Host, lastmod, extra)
	}))
	defer srv.Close()

	ctx := context.Background()
	st, _ := store.OpenMemory(ctx)
	defer st.Close()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	clock := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now }
	st.SetClock(clock)
	ref := &fakeRefresher{}
	var notes []string
	svc := New(Config{Store: st, Fetcher: feeds.NewFetcher(feeds.Options{AllowPrivate: true}), Refresher: ref,
		CheckInterval: time.Hour, MinGap: 5 * time.Second, DailyLimit: 100, Now: clock, Location: time.UTC,
		Notify: func(_ context.Context, s string) { notes = append(notes, s) }})

	src, r, err := svc.Add(ctx, feeds.Candidate{URL: srv.URL + "/sitemap.xml", Kind: feeds.KindSitemap}, false)
	if err != nil || r.New != 2 || r.Queued != 0 {
		t.Fatalf("baseline add: %+v %v", r, err)
	}
	// Page a changes, page c appears.
	mu.Lock()
	lastmod, extra = "2026-09-27", `<url><loc>`+srv.URL+`/c</loc></url>`
	mu.Unlock()
	src.ETag, src.LastModified = "", ""
	r, err = svc.Check(ctx, src)
	if err != nil || r.Changed != 1 || r.New != 1 || r.Queued != 2 {
		t.Fatalf("check: %+v %v", r, err)
	}
	// Drain the queue with a flood wait in the middle.
	ref.flood = true
	for i := 0; i < 4; i++ {
		svc.tick(ctx)
		clockMu.Lock()
		now = now.Add(time.Minute)
		clockMu.Unlock()
	}
	got := ref.got()
	if len(got) != 2 || !strings.HasSuffix(got[0], "/a") && !strings.HasSuffix(got[0], "/c") {
		t.Fatalf("refreshed = %v", got)
	}
	svc.tick(ctx) // empty queue → digest
	if len(notes) != 1 || !strings.Contains(notes[0], "2") {
		t.Fatalf("digest = %v", notes)
	}
	// A manual request jumps the queue and bypasses dedup.
	if _, err := svc.Enqueue(ctx, srv.URL+"/a", true); err != nil {
		t.Fatal(err)
	}
	svc.tick(ctx)
	if got = ref.got(); len(got) != 3 {
		t.Fatalf("manual refresh not processed: %v", got)
	}
}

func TestDailyLimit(t *testing.T) {
	ctx := context.Background()
	st, _ := store.OpenMemory(ctx)
	defer st.Close()
	ref := &fakeRefresher{}
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	st.SetClock(clock)
	svc := New(Config{Store: st, Fetcher: feeds.NewFetcher(feeds.Options{}), Refresher: ref, CheckInterval: time.Hour,
		MinGap: 5 * time.Second, DailyLimit: 2, Location: time.UTC, Now: clock})
	for _, u := range []string{"https://e.example/1", "https://e.example/2", "https://e.example/3"} {
		if _, err := svc.Enqueue(ctx, u, false); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		svc.tick(ctx)
		now = now.Add(time.Minute)
	}
	if n := len(ref.got()); n != 2 {
		t.Fatalf("refreshed %d, want daily limit 2", n)
	}
}

func TestFloodWaitDoesNotConsumeFailureBudget(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	st.SetClock(func() time.Time { return now })
	ref := &fakeRefresher{flood: true}
	svc := New(Config{Store: st, Refresher: ref, MinGap: time.Second, DailyLimit: 10, Location: time.UTC, Now: func() time.Time { return now }})
	if _, err := svc.Enqueue(ctx, "https://example.com/page", false); err != nil {
		t.Fatal(err)
	}
	svc.tick(ctx)
	u, err := st.NextQueued(ctx, now.Add(time.Hour))
	if err != nil || u.Attempts != 0 {
		t.Fatalf("flood wait consumed a failure attempt: %+v %v", u, err)
	}
}

func TestMinGapSurvivesWakeups(t *testing.T) {
	ctx := context.Background()
	st, _ := store.OpenMemory(ctx)
	defer st.Close()
	ref := &fakeRefresher{}
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	st.SetClock(clock)
	svc := New(Config{Store: st, Fetcher: feeds.NewFetcher(feeds.Options{}), Refresher: ref, CheckInterval: time.Hour,
		MinGap: 45 * time.Second, DailyLimit: 100, Location: time.UTC, Now: clock})
	_, _ = svc.Enqueue(ctx, "https://e.example/1", false)
	_, _ = svc.Enqueue(ctx, "https://e.example/2", false)
	svc.tick(ctx)
	now = now.Add(10 * time.Second) // an early wake-up
	if wait := svc.tick(ctx); wait < 30*time.Second || len(ref.got()) != 1 {
		t.Fatalf("gap not enforced: wait %v refreshed %d", wait, len(ref.got()))
	}
	// Re-queueing an already refreshed url is postponed by the 24h dedup.
	now = now.Add(time.Minute)
	svc.tick(ctx)
	_, _ = svc.Enqueue(ctx, "https://e.example/1", false)
	now = now.Add(time.Minute)
	svc.tick(ctx)
	if n := len(ref.got()); n != 2 {
		t.Fatalf("dedup failed: %v", ref.got())
	}
}
