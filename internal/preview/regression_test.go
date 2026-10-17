package preview

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"tadmin/internal/preview/feeds"
	"tadmin/internal/store"
)

func TestSourceBaselineMustSucceed(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	s := New(Config{Store: st, Fetcher: feeds.NewFetcher(feeds.Options{AllowPrivate: true})})
	if _, _, err := s.Add(ctx, feeds.Candidate{URL: srv.URL, Kind: feeds.KindSitemap}, false); err == nil {
		t.Fatal("failed baseline accepted")
	}
	sources, _ := st.ListSources(ctx)
	if len(sources) != 0 {
		t.Fatal("failed baseline left an enabled source that will queue existing pages")
	}
}

func TestIndexRechecksChildrenAndRejectsPartialWalk(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var childCalls atomic.Int32
	var broken atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Path == "/child" {
			childCalls.Add(1)
			if broken.Load() {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `<urlset><url><loc>https://example.com/article</loc></url></urlset>`)
			return
		}
		w.Header().Set("ETag", `"unchanged-index"`)
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(304)
			return
		}
		fmt.Fprintf(w, `<sitemapindex><sitemap><loc>http://%s/child</loc></sitemap></sitemapindex>`, r.Host)
	}))
	defer srv.Close()
	s := New(Config{Store: st, Fetcher: feeds.NewFetcher(feeds.Options{AllowPrivate: true})})
	src, _, err := s.Add(ctx, feeds.Candidate{URL: srv.URL, Kind: feeds.KindSitemapIndex}, false)
	if err != nil {
		t.Fatal(err)
	}
	before := childCalls.Load()
	if _, err := s.Check(ctx, src); err != nil {
		t.Fatal(err)
	}
	if childCalls.Load() == before {
		t.Error("unchanged index hid changes in children")
	}
	broken.Store(true)
	src.ETag = ""
	if _, err := s.Check(ctx, src); err == nil {
		t.Error("partial walk reported success")
	}
}

func TestHTMLIsNotSuccessfulSourceCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html><body>maintenance</body></html>") }))
	defer srv.Close()
	s := New(Config{Fetcher: feeds.NewFetcher(feeds.Options{AllowPrivate: true})})
	if _, _, err := s.collect(context.Background(), &store.PreviewSource{URL: srv.URL, Kind: string(feeds.KindSitemap)}); err == nil {
		t.Fatal("HTML silently replaced feed")
	}
}
