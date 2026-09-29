package feeds

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// testFetcher returns a Fetcher allowed to reach httptest servers.
func testFetcher(opts Options) *Fetcher {
	opts.AllowPrivate = true
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Second
	}
	return NewFetcher(opts)
}

// hitLog records request paths served by a test server.
type hitLog struct {
	mu    sync.Mutex
	paths []string
}

func (h *hitLog) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.paths = append(h.paths, r.URL.Path)
		h.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (h *hitLog) count(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, p := range h.paths {
		if p == path {
			n++
		}
	}
	return n
}

func newServer(t *testing.T, routes map[string]http.HandlerFunc) (*httptest.Server, *hitLog) {
	t.Helper()
	mux := http.NewServeMux()
	for p, h := range routes {
		mux.HandleFunc(p, h)
	}
	hits := &hitLog{}
	srv := httptest.NewServer(hits.wrap(mux))
	t.Cleanup(srv.Close)
	return srv, hits
}

func serve(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = w.Write([]byte(body))
	}
}

func serveBytes(header map[string]string, body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for k, v := range header {
			w.Header().Set(k, v)
		}
		_, _ = w.Write(body)
	}
}

func urlset(locs ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, l := range locs {
		fmt.Fprintf(&b, "<url><loc>%s</loc></url>", l)
	}
	b.WriteString(`</urlset>`)
	return b.String()
}

func sitemapIndex(locs ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, l := range locs {
		fmt.Fprintf(&b, "<sitemap><loc>%s</loc></sitemap>", l)
	}
	b.WriteString(`</sitemapindex>`)
	return b.String()
}

func entryURLs(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.URL
	}
	return out
}

func TestFetchConditional(t *testing.T) {
	const lastMod = "Mon, 02 Jan 2006 15:04:05 GMT"
	var gotUA, gotAE string
	srv, _ := newServer(t, map[string]http.HandlerFunc{
		"/feed.xml": func(w http.ResponseWriter, r *http.Request) {
			gotUA, gotAE = r.Header.Get("User-Agent"), r.Header.Get("Accept-Encoding")
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Last-Modified", lastMod)
			if r.Header.Get("If-None-Match") == `"v1"` || r.Header.Get("If-Modified-Since") == lastMod {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(`<rss><channel><item><link>/p1</link></item></channel></rss>`))
		},
	})
	f := testFetcher(Options{})
	ctx := context.Background()

	doc, err := f.Fetch(ctx, srv.URL+"/feed.xml#frag", Conditional{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.NotModified || doc.ETag != `"v1"` || doc.LastModified != lastMod || doc.Kind != KindRSS {
		t.Fatalf("first fetch: %+v", doc)
	}
	if want := srv.URL + "/p1"; len(doc.Entries) != 1 || doc.Entries[0].URL != want {
		t.Fatalf("entries = %v, want %s", entryURLs(doc.Entries), want)
	}
	if gotUA != DefaultUserAgent || gotAE != "gzip" {
		t.Errorf("User-Agent = %q, Accept-Encoding = %q", gotUA, gotAE)
	}

	for name, cond := range map[string]Conditional{
		"etag":          {ETag: doc.ETag},
		"last-modified": {LastModified: doc.LastModified},
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := f.Fetch(ctx, srv.URL+"/feed.xml", cond)
			if err != nil {
				t.Fatal(err)
			}
			if !doc.NotModified || doc.ETag != `"v1"` || doc.LastModified != lastMod || len(doc.Entries) != 0 {
				t.Fatalf("conditional fetch: %+v", doc)
			}
		})
	}
}

func TestFetchGzip(t *testing.T) {
	sm := []byte(urlset("https://example.com/gz"))
	srv, _ := newServer(t, map[string]http.HandlerFunc{
		"/ce.xml":        serveBytes(map[string]string{"Content-Encoding": "gzip", "Content-Type": "application/xml"}, gzipBytes(t, sm)),
		"/file.xml.gz":   serveBytes(map[string]string{"Content-Type": "application/x-gzip"}, gzipBytes(t, sm)),
		"/double.xml.gz": serveBytes(map[string]string{"Content-Encoding": "gzip", "Content-Type": "application/gzip"}, gzipBytes(t, gzipBytes(t, sm))),
		"/lying.xml":     serveBytes(map[string]string{"Content-Encoding": "gzip"}, sm),
	})
	f := testFetcher(Options{})
	for _, p := range []string{"/ce.xml", "/file.xml.gz", "/double.xml.gz", "/lying.xml"} {
		t.Run(p, func(t *testing.T) {
			doc, err := f.Fetch(context.Background(), srv.URL+p, Conditional{})
			if err != nil {
				t.Fatal(err)
			}
			if doc.Kind != KindSitemap || len(doc.Entries) != 1 || doc.Entries[0].URL != "https://example.com/gz" {
				t.Fatalf("doc = %+v", doc)
			}
		})
	}
}

func TestFetchSizeLimit(t *testing.T) {
	const limit = 1 << 20
	bomb := gzipBytes(t, make([]byte, 32<<20)) // ~32 KiB compressed, 32 MiB inflated
	big := []byte(urlset(strings.Repeat("x", 2*limit)))
	srv, _ := newServer(t, map[string]http.HandlerFunc{
		"/bomb-ce.xml": serveBytes(map[string]string{"Content-Encoding": "gzip"}, bomb),
		"/bomb.xml.gz": serveBytes(map[string]string{"Content-Type": "application/x-gzip"}, bomb),
		"/big.xml":     serveBytes(nil, big),
		"/chunked.xml": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(big[:1])
			w.(http.Flusher).Flush()
			_, _ = w.Write(big[1:])
		},
	})
	f := testFetcher(Options{MaxBodyBytes: limit})
	for _, p := range []string{"/bomb-ce.xml", "/bomb.xml.gz", "/big.xml", "/chunked.xml"} {
		t.Run(p, func(t *testing.T) {
			_, err := f.Fetch(context.Background(), srv.URL+p, Conditional{})
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("err = %v, want ErrTooLarge", err)
			}
		})
	}
}

func TestFetchStatusError(t *testing.T) {
	srv, _ := newServer(t, map[string]http.HandlerFunc{
		"/gone": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "gone", http.StatusGone) },
	})
	f := testFetcher(Options{})
	for path, code := range map[string]int{"/missing": http.StatusNotFound, "/gone": http.StatusGone} {
		_, err := f.Fetch(context.Background(), srv.URL+path, Conditional{})
		var se *StatusError
		if !errors.Is(err, ErrHTTPStatus) || !errors.As(err, &se) || se.Code != code {
			t.Errorf("%s: err = %v, want StatusError %d", path, err, code)
		}
	}
}

func TestFetchRedirects(t *testing.T) {
	srv, hits := newServer(t, map[string]http.HandlerFunc{
		"/loop/": func(w http.ResponseWriter, r *http.Request) {
			var n int
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/loop/"), "%d", &n)
			http.Redirect(w, r, fmt.Sprintf("/loop/%d", n+1), http.StatusFound)
		},
		"/ftp": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "ftp://example.com/sitemap.xml", http.StatusMovedPermanently)
		},
		"/old": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/new/feed.xml", http.StatusMovedPermanently)
		},
		"/new/feed.xml": serve("application/atom+xml",
			`<feed xmlns="http://www.w3.org/2005/Atom"><entry><link href="post"/></entry></feed>`),
	})
	ctx := context.Background()

	t.Run("limit", func(t *testing.T) {
		f := testFetcher(Options{MaxRedirects: 3})
		_, err := f.Fetch(ctx, srv.URL+"/loop/0", Conditional{})
		if !errors.Is(err, ErrTooManyRedirects) {
			t.Fatalf("err = %v, want ErrTooManyRedirects", err)
		}
		if hits.count("/loop/3") != 1 || hits.count("/loop/4") != 0 {
			t.Fatalf("followed wrong number of redirects: %v", hits.paths)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		f := testFetcher(Options{MaxRedirects: -1})
		if _, err := f.Fetch(ctx, srv.URL+"/old", Conditional{}); !errors.Is(err, ErrTooManyRedirects) {
			t.Fatalf("err = %v, want ErrTooManyRedirects", err)
		}
	})
	t.Run("non-http scheme", func(t *testing.T) {
		f := testFetcher(Options{})
		if _, err := f.Fetch(ctx, srv.URL+"/ftp", Conditional{}); !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("err = %v, want ErrInvalidURL", err)
		}
	})
	t.Run("final URL and relative resolution", func(t *testing.T) {
		f := testFetcher(Options{})
		doc, err := f.Fetch(ctx, srv.URL+"/old", Conditional{})
		if err != nil {
			t.Fatal(err)
		}
		if doc.FinalURL != srv.URL+"/new/feed.xml" {
			t.Errorf("FinalURL = %q", doc.FinalURL)
		}
		if got := entryURLs(doc.Entries); len(got) != 1 || got[0] != srv.URL+"/new/post" {
			t.Errorf("entries = %v", got)
		}
	})
}

func TestFetchInvalidURL(t *testing.T) {
	f := testFetcher(Options{})
	for _, u := range []string{"", "ftp://example.com/x", "file:///etc/passwd", "javascript:alert(1)", "http://", "/relative"} {
		if _, err := f.Fetch(context.Background(), u, Conditional{}); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("Fetch(%q) err = %v, want ErrInvalidURL", u, err)
		}
	}
}

func TestFetchBlocksPrivateAddresses(t *testing.T) {
	srv, hits := newServer(t, map[string]http.HandlerFunc{"/sitemap.xml": serve("", urlset("https://example.com/"))})
	f := NewFetcher(Options{Timeout: 5 * time.Second}) // AllowPrivate=false
	ctx := context.Background()
	port := strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)

	for _, u := range []string{
		srv.URL + "/sitemap.xml",
		"http://localhost:" + port + "/sitemap.xml",
		"http://[::1]:" + port + "/sitemap.xml",
		"http://0.0.0.0:" + port + "/sitemap.xml",
		"http://169.254.169.254/latest/meta-data/",
	} {
		if _, err := f.Fetch(ctx, u, Conditional{}); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("Fetch(%s) err = %v, want ErrBlockedAddress", u, err)
		}
	}
	if _, err := f.Walk(ctx, srv.URL+"/sitemap.xml", 0); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("Walk err = %v, want ErrBlockedAddress", err)
	}
	if _, err := f.Discover(ctx, srv.URL); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("Discover err = %v, want ErrBlockedAddress", err)
	}
	if n := len(hits.paths); n != 0 {
		t.Errorf("server received %d requests despite blocking", n)
	}

	// The same server is reachable with AllowPrivate or a custom client.
	if _, err := testFetcher(Options{}).Fetch(ctx, srv.URL+"/sitemap.xml", Conditional{}); err != nil {
		t.Errorf("AllowPrivate fetch: %v", err)
	}
	if _, err := NewFetcher(Options{HTTPClient: srv.Client()}).Fetch(ctx, srv.URL+"/sitemap.xml", Conditional{}); err != nil {
		t.Errorf("HTTPClient override fetch: %v", err)
	}
}

func TestIsBlockedIP(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1":            true,
		"127.1.2.3":            true,
		"10.1.2.3":             true,
		"172.16.0.1":           true,
		"172.31.255.255":       true,
		"192.168.1.1":          true,
		"169.254.169.254":      true,
		"100.64.0.1":           true,
		"100.127.255.255":      true,
		"0.0.0.0":              true,
		"0.1.2.3":              true,
		"224.0.0.1":            true,
		"255.255.255.255":      true,
		"198.18.0.1":           true,
		"::":                   true,
		"::1":                  true,
		"fe80::1":              true,
		"fc00::1":              true,
		"fd12:3456::1":         true,
		"ff02::1":              true,
		"::ffff:127.0.0.1":     true,
		"::ffff:10.0.0.1":      true,
		"64:ff9b::a00:1":       true, // NAT64 of 10.0.0.1
		"2002:7f00:1::":        true, // 6to4 of 127.0.0.1
		"8.8.8.8":              false,
		"1.1.1.1":              false,
		"100.63.255.255":       false,
		"100.128.0.0":          false,
		"172.32.0.1":           false,
		"2606:4700:4700::1111": false,
		"::ffff:8.8.8.8":       false,
		"64:ff9b::808:808":     false,
	}
	for s, want := range tests {
		if got := isBlockedIP(netip.MustParseAddr(s)); got != want {
			t.Errorf("isBlockedIP(%s) = %v, want %v", s, got, want)
		}
	}
}
