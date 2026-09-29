package feeds

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Fetcher defaults.
const (
	DefaultUserAgent    = "TAdminBot/2 (+link preview refresher)"
	DefaultTimeout      = 20 * time.Second
	DefaultMaxRedirects = 5
)

const acceptSources = "application/xml, text/xml;q=0.9, application/rss+xml, application/atom+xml, " +
	"application/feed+json, application/json;q=0.8, text/html;q=0.7, */*;q=0.5"

// Options configures a Fetcher. Zero values select the defaults.
type Options struct {
	// UserAgent defaults to DefaultUserAgent.
	UserAgent string
	// Timeout bounds each request including reading the body; default 20s.
	Timeout time.Duration
	// MaxBodyBytes limits the body after gzip decompression; default 20 MiB.
	MaxBodyBytes int64
	// MaxRedirects defaults to 5; a negative value disables redirects.
	MaxRedirects int
	// AllowPrivate permits loopback, private, link-local, CGNAT and other
	// non-public destinations. By default they are refused at dial time
	// with ErrBlockedAddress.
	AllowPrivate bool
	// HTTPClient overrides the HTTP client (for tests). The SSRF-checking
	// dialer is then not applied; if its CheckRedirect is nil the Fetcher's
	// redirect policy is installed on a copy.
	HTTPClient *http.Client
}

// Fetcher fetches and parses sources. It is safe for concurrent use.
type Fetcher struct {
	opts         Options
	maxRedirects int
	client       *http.Client
}

// NewFetcher returns a Fetcher configured by opts.
func NewFetcher(opts Options) *Fetcher {
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultUserAgent
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	f := &Fetcher{opts: opts, maxRedirects: opts.MaxRedirects}
	switch {
	case opts.MaxRedirects == 0:
		f.maxRedirects = DefaultMaxRedirects
	case opts.MaxRedirects < 0:
		f.maxRedirects = 0
	}
	if opts.HTTPClient != nil {
		c := *opts.HTTPClient
		if c.CheckRedirect == nil {
			c.CheckRedirect = f.checkRedirect
		}
		f.client = &c
	} else {
		f.client = &http.Client{
			Transport: &http.Transport{
				Proxy:                 nil, // a proxy would bypass the dial-time address check
				DialContext:           newSafeDialer(opts.AllowPrivate).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          32,
				MaxIdleConnsPerHost:   4,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: time.Second,
				DisableCompression:    true, // gzip is decoded here so the limit applies post-decompression
			},
			CheckRedirect: f.checkRedirect,
		}
	}
	return f
}

func (f *Fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if err := checkHTTPURL(req.URL); err != nil {
		return fmt.Errorf("redirect to %s: %w", req.URL.Redacted(), err)
	}
	if len(via) > f.maxRedirects {
		return fmt.Errorf("%w: stopped after %d redirects", ErrTooManyRedirects, f.maxRedirects)
	}
	return nil
}

// Conditional carries validators from a previous fetch.
type Conditional struct {
	ETag         string
	LastModified string
}

// Fetch GETs rawURL (http/https only) with If-None-Match/If-Modified-Since
// from cond, decodes gzip (Content-Encoding or gzip magic bytes, e.g. *.gz
// sitemaps) and parses the body. A 304 response yields a Document with
// NotModified set. Non-2xx statuses yield a *StatusError.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, cond Conditional) (*Document, error) {
	resp, err := f.get(ctx, rawURL, cond, f.opts.MaxBodyBytes, acceptSources)
	if err != nil {
		return nil, err
	}
	if resp.notModified {
		return &Document{
			NotModified:  true,
			ETag:         firstNonEmpty(resp.etag, cond.ETag),
			LastModified: firstNonEmpty(resp.lastModified, cond.LastModified),
			FinalURL:     resp.finalURL,
		}, nil
	}
	doc, err := parse(resp.body, resp.contentType, resp.finalURL, f.opts.MaxBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", resp.finalURL, err)
	}
	doc.ETag, doc.LastModified, doc.FinalURL = resp.etag, resp.lastModified, resp.finalURL
	return doc, nil
}

type rawResponse struct {
	body         []byte
	contentType  string
	etag         string
	lastModified string
	finalURL     string
	notModified  bool
}

// get performs a GET and returns the Content-Encoding-decoded body, read up
// to limit bytes.
func (f *Fetcher) get(ctx context.Context, rawURL string, cond Conditional, limit int64, accept string) (*rawResponse, error) {
	u, err := parseHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}
	u.Fragment, u.RawFragment = "", ""
	ctx, cancel := context.WithTimeout(ctx, f.opts.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	req.Header.Set("User-Agent", f.opts.UserAgent)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Encoding", "gzip")
	if cond.ETag != "" {
		req.Header.Set("If-None-Match", cond.ETag)
	}
	if cond.LastModified != "" {
		req.Header.Set("If-Modified-Since", cond.LastModified)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feeds: fetch: %w", err)
	}
	defer resp.Body.Close()

	out := &rawResponse{
		contentType:  resp.Header.Get("Content-Type"),
		etag:         resp.Header.Get("ETag"),
		lastModified: resp.Header.Get("Last-Modified"),
		finalURL:     resp.Request.URL.String(),
	}
	switch {
	case resp.StatusCode == http.StatusNotModified:
		out.notModified = true
		return out, nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &StatusError{Code: resp.StatusCode, URL: out.finalURL}
	}
	body, err := readBody(resp, limit)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", out.finalURL, err)
	}
	out.body = body
	return out, nil
}

// readBody decodes Content-Encoding and reads at most limit decoded bytes.
func readBody(resp *http.Response, limit int64) ([]byte, error) {
	ce := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if ce == "" || ce == "identity" {
		if resp.ContentLength > limit {
			return nil, fmt.Errorf("%w: Content-Length %d exceeds %d bytes", ErrTooLarge, resp.ContentLength, limit)
		}
		return readLimited(resp.Body, limit)
	}
	if ce != "gzip" && ce != "x-gzip" {
		return nil, fmt.Errorf("%w: content encoding %q", ErrUnsupported, ce)
	}
	br := bufio.NewReader(resp.Body)
	if magic, _ := br.Peek(2); len(magic) < 2 || magic[0] != 0x1f || magic[1] != 0x8b {
		// Misconfigured server: labelled gzip but sent as-is.
		return readLimited(br, limit)
	}
	zr, err := gzip.NewReader(br)
	if err != nil {
		return nil, fmt.Errorf("feeds: invalid gzip body: %w", err)
	}
	defer zr.Close()
	return readLimited(zr, limit)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
