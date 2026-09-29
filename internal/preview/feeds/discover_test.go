package feeds

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

const rssBody = `<?xml version="1.0"?><rss version="2.0"><channel><title>Site RSS</title><item><link>/p</link></item></channel></rss>`

func candidatesString(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = string(c.Kind) + " " + c.URL
	}
	return out
}

func TestDiscoverSourceItself(t *testing.T) {
	srv, hits := newServer(t, map[string]http.HandlerFunc{"/feed.xml": serve("text/xml", rssBody)})
	got, err := testFetcher(Options{}).Discover(context.Background(), srv.URL+"/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{{URL: srv.URL + "/feed.xml", Kind: KindRSS, Title: "Site RSS"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(hits.paths) != 1 {
		t.Errorf("expected a single request, got %v", hits.paths)
	}
}

func TestDiscoverRobotsAndHTML(t *testing.T) {
	var base string
	home := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Home</title>
<link rel="alternate" type="application/rss+xml" title="Main feed" href="/feed.rss">
<link rel="alternate" type="application/atom+xml" href="/broken-atom">
<link rel="alternate" type="application/feed+json" title="JSON" href="` + base + `/feed.json">
<link rel="sitemap" type="application/xml" href="/sitemap.xml">
<link rel="alternate" type="application/rss+xml" href="/feed.rss#again">
</head><body></body></html>`))
	}
	srv, hits := newServer(t, map[string]http.HandlerFunc{
		"/": home,
		"/robots.txt": serve("text/plain", "User-agent: *\nDisallow: /private # comment\n"+
			"Sitemap: /robots-index.xml\nsitemap:   /robots-missing.xml  \nSitemap: /sitemap.xml\n"),
		"/robots-index.xml": serve("application/xml", sitemapIndex("/sitemap.xml")),
		"/sitemap.xml":      serve("application/xml", urlset("/a")),
		"/feed.rss":         serve("application/rss+xml", rssBody),
		"/feed.json":        serve("application/feed+json", `{"version":"https://jsonfeed.org/version/1.1","title":"JF","items":[]}`),
	})
	base = srv.URL
	f := testFetcher(Options{})

	for _, start := range []string{srv.URL, srv.URL + "/blog/missing-post"} {
		t.Run(start, func(t *testing.T) {
			got, err := f.Discover(context.Background(), start)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{
				"sitemapindex " + base + "/robots-index.xml",
				"sitemap " + base + "/sitemap.xml",
				"rss " + base + "/feed.rss",
				"jsonfeed " + base + "/feed.json",
			}
			if !slices.Equal(candidatesString(got), want) {
				t.Fatalf("got %v\nwant %v", candidatesString(got), want)
			}
			if got[2].Title != "Site RSS" || got[3].Title != "JF" {
				t.Errorf("titles = %q, %q", got[2].Title, got[3].Title)
			}
		})
	}
	for _, p := range append(slices.Clone(sitemapProbePaths[1:]), feedProbePaths[:len(feedProbePaths)-1]...) { // /sitemap.xml and /feed.json are declared
		if hits.count(p) != 0 {
			t.Errorf("probed %s although sources were declared", p)
		}
	}
	if hits.count("/feed.rss") != 2 {
		t.Errorf("/feed.rss fetched %d times over two discoveries, want 2", hits.count("/feed.rss"))
	}
}

func TestDiscoverProbes(t *testing.T) {
	srv, hits := newServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			// Soft-404: every unknown path returns the HTML home page.
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><title>Plain</title></head><body>hi</body></html>`))
		},
		"/sitemap.xml": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"/sitemap_index.xml": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(gzipBytes(t, []byte(sitemapIndex("/sitemap-1.xml"))))
		},
		"/feed/":   serve("application/rss+xml", rssBody),
		"/rss.xml": serve("application/rss+xml", rssBody),
	})
	got, err := testFetcher(Options{}).Discover(context.Background(), strings.TrimPrefix(srv.URL, "http://")+"/")
	if err == nil {
		t.Fatalf("scheme-less URL should default to https and fail against a plain HTTP server, got %v", got)
	}

	got, err = testFetcher(Options{}).Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"sitemapindex " + srv.URL + "/sitemap_index.xml",
		"rss " + srv.URL + "/feed/", // /feed redirects to /feed/
	}
	if !slices.Equal(candidatesString(got), want) {
		t.Fatalf("got %v\nwant %v", candidatesString(got), want)
	}
	for _, p := range []string{"/wp-sitemap.xml", "/sitemap-index.xml", "/rss.xml", "/atom.xml"} {
		if hits.count(p) != 0 {
			t.Errorf("kept probing %s after a hit", p)
		}
	}
}

func TestDiscoverNothing(t *testing.T) {
	srv, _ := newServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`<html><head><link rel="alternate" type="application/rss+xml" href="/nope.xml"></head></html>`))
		},
	})
	_, err := testFetcher(Options{}).Discover(context.Background(), srv.URL)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestDiscoverUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	_, err := testFetcher(Options{}).Discover(context.Background(), u)
	if err == nil || errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want a network error", err)
	}
}

func TestDiscoverTLSWithCustomClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/atom.xml" {
			w.Header().Set("Content-Type", "application/atom+xml")
			_, _ = w.Write([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"><title>T</title></feed>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	f := NewFetcher(Options{HTTPClient: srv.Client()})
	got, err := f.Discover(context.Background(), strings.TrimPrefix(srv.URL, "https://")+"/atom.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindAtom || got[0].URL != srv.URL+"/atom.xml" {
		t.Fatalf("got %v", got)
	}
}

func TestParseRobotsSitemaps(t *testing.T) {
	base := mustURL(t, "https://example.com/robots.txt")
	body := "\ufeffUser-agent: *\r\nSITEMAP: https://cdn.example.net/s.xml\r\n# Sitemap: /commented.xml\n" +
		"Sitemap:/rel.xml\nSitemap: ftp://example.com/x\nSitemap: https://cdn.example.net/s.xml\nAllow: /\n"
	got := parseRobotsSitemaps([]byte(body), base)
	want := []string{"https://cdn.example.net/s.xml", "https://example.com/rel.xml"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
