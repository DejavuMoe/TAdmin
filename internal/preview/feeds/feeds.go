// Package feeds discovers, fetches and parses link sources — XML sitemaps,
// sitemap indexes, RSS (0.9x, 1.0/RDF, 2.0), Atom, JSON Feed and HTML pages
// that advertise them — so callers can detect new or changed page URLs.
//
// Parse is a pure function. Fetcher adds an HTTP client with conditional
// requests, gzip handling, body size limits and SSRF protection enforced at
// dial time.
package feeds

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Kind identifies the format of a parsed document or candidate source.
type Kind string

// Supported document kinds.
const (
	KindSitemap      Kind = "sitemap"      // <urlset>
	KindSitemapIndex Kind = "sitemapindex" // <sitemapindex>
	KindRSS          Kind = "rss"          // RSS 0.9x/2.0 and RDF/RSS 1.0
	KindAtom         Kind = "atom"
	KindJSONFeed     Kind = "jsonfeed"
	KindHTML         Kind = "html"
)

// IsSource reports whether k is a machine-readable link source (a sitemap,
// sitemap index or feed), as opposed to an HTML page or an unknown kind.
func (k Kind) IsSource() bool {
	switch k {
	case KindSitemap, KindSitemapIndex, KindRSS, KindAtom, KindJSONFeed:
		return true
	}
	return false
}

// MaxEntriesPerFile caps the number of entries (or child sitemaps) taken from
// a single document, matching the sitemap protocol limit.
const MaxEntriesPerFile = 50000

// Entry is a page URL found in a document.
type Entry struct {
	// URL is absolute and normalized: resolved against the document URL,
	// whitespace trimmed, fragment stripped, scheme/host lowercased and the
	// default port dropped.
	URL string
	// Updated is lastmod / updated / published / pubDate / date_modified in
	// UTC; zero if unknown.
	Updated time.Time
	// Title is optional.
	Title string
}

// Candidate is a discovered link source.
type Candidate struct {
	URL   string
	Kind  Kind
	Title string
}

// Document is the result of parsing (and optionally fetching) a source.
type Document struct {
	Kind  Kind
	Title string
	// Entries holds page URLs. It is empty for sitemap indexes and HTML.
	Entries []Entry
	// Children holds child sitemap URLs of a sitemap index.
	Children []Entry
	// Alternates holds feed/sitemap links advertised by an HTML page. They
	// are not verified.
	Alternates []Candidate

	ETag         string
	LastModified string
	// NotModified is set for a 304 response; Kind and entries are then empty.
	NotModified bool
	// FinalURL is the document URL after redirects.
	FinalURL string
}

// Sentinel errors. Returned errors wrap these; test with errors.Is.
var (
	// ErrTooLarge means the (decompressed) body exceeded the size limit.
	ErrTooLarge = errors.New("feeds: document too large")
	// ErrUnsupported means the content is not a sitemap or feed (or uses an
	// unsupported encoding).
	ErrUnsupported = errors.New("feeds: not a supported sitemap or feed")
	// ErrBlockedAddress means the destination resolved to an address that
	// is not allowed (loopback, private, link-local, ...).
	ErrBlockedAddress = errors.New("feeds: destination address not allowed")
	// ErrHTTPStatus means the server answered with a non-2xx, non-304
	// status. The concrete error is a *StatusError.
	ErrHTTPStatus = errors.New("feeds: unexpected HTTP status")
	// ErrInvalidURL means the URL is malformed or not http(s).
	ErrInvalidURL = errors.New("feeds: invalid or non-http(s) URL")
	// ErrTooManyRedirects means the redirect limit was exceeded.
	ErrTooManyRedirects = errors.New("feeds: too many redirects")
)

// StatusError reports an unexpected HTTP status. It wraps ErrHTTPStatus.
type StatusError struct {
	Code int
	URL  string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("feeds: GET %s: unexpected HTTP status %d %s", e.URL, e.Code, http.StatusText(e.Code))
}

// Unwrap returns ErrHTTPStatus.
func (e *StatusError) Unwrap() error { return ErrHTTPStatus }

// NormalizeURL canonicalizes an http(s) URL for dedupe: it lowercases the
// scheme and host, drops the default port and the fragment, turns an empty
// path into "/", and keeps path and query otherwise as-is. Non-http(s) and
// host-less URLs are rejected with ErrInvalidURL.
func NormalizeURL(raw string) (string, error) {
	u, err := parseHTTPURL(raw)
	if err != nil {
		return "", err
	}
	return normalizeParsed(u), nil
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if err := checkHTTPURL(u); err != nil {
		return nil, err
	}
	return u, nil
}

func checkHTTPURL(u *url.URL) error {
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: unsupported scheme %q", ErrInvalidURL, u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%w: missing host in %q", ErrInvalidURL, u.Redacted())
	}
	return nil
}

func normalizeParsed(u *url.URL) string {
	v := *u
	v.Scheme = strings.ToLower(v.Scheme)
	host := strings.ToLower(v.Hostname())
	port := v.Port()
	if (v.Scheme == "http" && port == "80") || (v.Scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	v.Host = host
	v.Fragment, v.RawFragment = "", ""
	if v.Path == "" && v.RawPath == "" && v.Opaque == "" {
		v.Path = "/"
	}
	return v.String()
}

// resolveRef resolves ref against base (which may be nil) and returns the
// normalized absolute URL, or false if the result is not a usable http(s)
// URL.
func resolveRef(base *url.URL, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", false
	}
	if base != nil {
		r = base.ResolveReference(r)
	}
	if checkHTTPURL(r) != nil {
		return "", false
	}
	return normalizeParsed(r), true
}

// looksLikeURL reports whether s is an absolute http(s) URL.
func looksLikeURL(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// cleanText collapses runs of whitespace and trims the result.
func cleanText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// entrySet accumulates entries, deduplicating by URL and preserving the
// first occurrence, up to limit entries (0 = unlimited).
type entrySet struct {
	seen      map[string]struct{}
	items     []Entry
	limit     int
	truncated bool
}

func newEntrySet(limit int) *entrySet {
	return &entrySet{seen: make(map[string]struct{}), limit: limit}
}

func (s *entrySet) full() bool { return s.limit > 0 && len(s.items) >= s.limit }

// add appends e unless its URL was already seen. It returns false when e was
// rejected because the set is full.
func (s *entrySet) add(e Entry) bool {
	if _, dup := s.seen[e.URL]; dup {
		return true
	}
	if s.full() {
		s.truncated = true
		return false
	}
	s.seen[e.URL] = struct{}{}
	s.items = append(s.items, e)
	return true
}
