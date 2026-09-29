package feeds

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"
)

func TestWalkSitemapIndex(t *testing.T) {
	var base string
	gz := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-gzip")
			_, _ = w.Write(gzipBytes(t, []byte(body)))
		}
	}
	dyn := func(f func() string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(f())) }
	}
	srv, hits := newServer(t, map[string]http.HandlerFunc{
		"/index.xml": dyn(func() string {
			return sitemapIndex("/s1.xml", base+"/nested.xml", "/missing.xml", "/s1.xml#dup", "/page.html")
		}),
		"/s1.xml":     dyn(func() string { return urlset(base+"/a", base+"/b") }),
		"/nested.xml": dyn(func() string { return sitemapIndex("/s2.xml.gz", "/index.xml") }),
		"/s2.xml.gz":  func(w http.ResponseWriter, r *http.Request) { gz(urlset(base+"/b", base+"/c"))(w, r) },
		"/page.html":  serve("text/html", "<html><head><title>x</title></head></html>"),
	})
	base = srv.URL
	f := testFetcher(Options{})

	res, err := f.Walk(context.Background(), srv.URL+"/index.xml", 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{base + "/a", base + "/b", base + "/c"}; !slices.Equal(entryURLs(res.Entries), want) {
		t.Fatalf("entries = %v, want %v", entryURLs(res.Entries), want)
	}
	if res.Kind != KindSitemapIndex || res.Fetched != 5 || res.Truncated {
		t.Errorf("Kind = %q Fetched = %d Truncated = %v", res.Kind, res.Fetched, res.Truncated)
	}
	if len(res.Errors) != 2 {
		t.Fatalf("errors = %v, want 2", res.Errors)
	}
	var se *StatusError
	if !errors.As(res.Errors[0], &se) || se.Code != http.StatusNotFound {
		t.Errorf("errors[0] = %v, want 404", res.Errors[0])
	}
	if !errors.Is(res.Errors[1], ErrUnsupported) {
		t.Errorf("errors[1] = %v, want ErrUnsupported", res.Errors[1])
	}
	if hits.count("/index.xml") != 1 || hits.count("/s1.xml") != 1 {
		t.Errorf("documents fetched more than once: %v", hits.paths)
	}

	t.Run("maxURLs", func(t *testing.T) {
		res, err := f.Walk(context.Background(), srv.URL+"/index.xml", 2)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{base + "/a", base + "/b"}; !slices.Equal(entryURLs(res.Entries), want) || !res.Truncated {
			t.Fatalf("entries = %v truncated = %v", entryURLs(res.Entries), res.Truncated)
		}
		if res.Fetched != 2 {
			t.Errorf("Fetched = %d, want 2 (stop fetching once full)", res.Fetched)
		}
	})
}

func TestWalkDepthLimit(t *testing.T) {
	routes := map[string]http.HandlerFunc{}
	for i := range MaxWalkDepth + 1 {
		routes[fmt.Sprintf("/d%d.xml", i)] = serve("", sitemapIndex(fmt.Sprintf("/d%d.xml", i+1)))
	}
	routes[fmt.Sprintf("/d%d.xml", MaxWalkDepth+1)] = serve("", urlset("https://example.com/deep"))
	srv, hits := newServer(t, routes)

	res, err := testFetcher(Options{}).Walk(context.Background(), srv.URL+"/d0.xml", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 || len(res.Errors) != 1 || res.Fetched != MaxWalkDepth+1 {
		t.Fatalf("entries = %d errors = %v fetched = %d", len(res.Entries), res.Errors, res.Fetched)
	}
	if hits.count(fmt.Sprintf("/d%d.xml", MaxWalkDepth+1)) != 0 {
		t.Error("fetched beyond max depth")
	}
}

func TestWalkFeedAndErrors(t *testing.T) {
	srv, _ := newServer(t, map[string]http.HandlerFunc{
		"/feed.json": serve("application/feed+json",
			`{"version":"https://jsonfeed.org/version/1","items":[{"id":"1","url":"/one"},{"id":"2","url":"/two"}]}`),
		"/page": serve("text/html", "<html><head></head><body></body></html>"),
	})
	f := testFetcher(Options{})
	ctx := context.Background()

	res, err := f.Walk(ctx, srv.URL+"/feed.json", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != KindJSONFeed || len(res.Entries) != 2 || res.Fetched != 1 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := f.Walk(ctx, srv.URL+"/page", 0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Walk(html) err = %v, want ErrUnsupported", err)
	}
	if _, err := f.Walk(ctx, srv.URL+"/missing", 0); !errors.Is(err, ErrHTTPStatus) {
		t.Errorf("Walk(404) err = %v, want ErrHTTPStatus", err)
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.Walk(cctx, srv.URL+"/feed.json", 0); !errors.Is(err, context.Canceled) {
		t.Errorf("Walk(cancelled) err = %v, want context.Canceled", err)
	}
}
