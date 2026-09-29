package feeds

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Discovery limits and probe paths.
const (
	robotsMaxBytes        = 512 << 10
	maxRobotsSitemaps     = 20
	maxDeclaredCandidates = 16
)

var (
	sitemapProbePaths = []string{"/sitemap.xml", "/sitemap_index.xml", "/wp-sitemap.xml", "/sitemap-index.xml"}
	feedProbePaths    = []string{"/feed", "/rss.xml", "/atom.xml", "/index.xml", "/feed.xml", "/feed.json"}
)

// Discover finds link sources for a site, page or feed URL. A URL without
// a scheme is treated as https. Candidates are verified (fetched and
// parsed) and ranked sitemaps first, then feeds, otherwise in discovery
// order:
//
//   - if the URL itself is a sitemap or feed, it is the only candidate;
//   - robots.txt Sitemap: lines on the (post-redirect) origin;
//   - HTML <link rel="alternate"> feeds and <link rel="sitemap">;
//   - same-origin probes of common sitemap and feed paths, stopping at the
//     first hit of each group.
//
// When nothing is found the error wraps ErrUnsupported.
func (f *Fetcher) Discover(ctx context.Context, rawURL string) ([]Candidate, error) {
	raw := strings.TrimSpace(rawURL)
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := parseHTTPURL(raw)
	if err != nil {
		return nil, err
	}
	d := &discovery{f: f, seen: make(map[string]bool)}
	origin := &url.URL{Scheme: strings.ToLower(u.Scheme), Host: u.Host, Path: "/"}

	var pageErr error
	var alternates []Candidate
	doc, err := f.Fetch(ctx, u.String(), Conditional{})
	switch {
	case err == nil && doc.Kind.IsSource():
		final, _ := NormalizeURL(doc.FinalURL)
		return []Candidate{{URL: final, Kind: doc.Kind, Title: doc.Title}}, nil
	case err == nil:
		alternates = doc.Alternates
		if fu, perr := url.Parse(doc.FinalURL); perr == nil && checkHTTPURL(fu) == nil {
			origin = &url.URL{Scheme: fu.Scheme, Host: fu.Host, Path: "/"}
		}
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(err, ErrHTTPStatus), errors.Is(err, ErrUnsupported), errors.Is(err, ErrTooLarge):
		// The page itself is unusable but the origin answered; keep looking.
		pageErr = err
	default:
		return nil, err // unreachable, blocked, bad URL, ...
	}
	d.markSeen(u.String())
	d.markSeen(origin.String())

	// robots.txt
	for _, sm := range f.robotsSitemaps(ctx, origin) {
		d.verify(ctx, sm, "", true)
	}
	// HTML alternates; fall back to the home page when the given page
	// offered none.
	pageNorm, originNorm := normalizeParsed(u), normalizeParsed(origin)
	if len(alternates) == 0 && pageNorm != originNorm && ctx.Err() == nil {
		if home, err := f.Fetch(ctx, origin.String(), Conditional{}); err == nil {
			if home.Kind.IsSource() {
				d.add(home, "")
			}
			alternates = home.Alternates
		}
	}
	for _, alt := range alternates {
		d.verify(ctx, alt.URL, alt.Title, true)
	}
	// Probes.
	if !d.has(isSitemapKind) {
		for _, p := range sitemapProbePaths {
			if d.verify(ctx, origin.ResolveReference(&url.URL{Path: p}).String(), "", false) {
				break
			}
		}
	}
	if !d.has(isFeedKind) {
		for _, p := range feedProbePaths {
			if d.verify(ctx, origin.ResolveReference(&url.URL{Path: p}).String(), "", false) {
				break
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(d.out) == 0 {
		if pageErr != nil {
			return nil, fmt.Errorf("%w: no sitemap or feed found for %s (page: %v)", ErrUnsupported, u.Redacted(), pageErr)
		}
		return nil, fmt.Errorf("%w: no sitemap or feed found for %s", ErrUnsupported, u.Redacted())
	}
	sort.SliceStable(d.out, func(i, j int) bool {
		return isSitemapKind(d.out[i].Kind) && !isSitemapKind(d.out[j].Kind)
	})
	return d.out, nil
}

func isSitemapKind(k Kind) bool { return k == KindSitemap || k == KindSitemapIndex }
func isFeedKind(k Kind) bool    { return k == KindRSS || k == KindAtom || k == KindJSONFeed }

type discovery struct {
	f        *Fetcher
	seen     map[string]bool
	out      []Candidate
	declared int
}

func (d *discovery) markSeen(raw string) {
	if u, err := NormalizeURL(raw); err == nil {
		d.seen[u] = true
	}
}

func (d *discovery) has(pred func(Kind) bool) bool {
	for _, c := range d.out {
		if pred(c.Kind) {
			return true
		}
	}
	return false
}

// verify fetches raw and records it if it parses as a source. declared
// candidates (robots.txt, HTML links) share a budget; probes do not.
func (d *discovery) verify(ctx context.Context, raw, title string, declared bool) bool {
	if ctx.Err() != nil {
		return false
	}
	u, err := NormalizeURL(raw)
	if err != nil || d.seen[u] {
		return false
	}
	d.seen[u] = true
	if declared {
		if d.declared >= maxDeclaredCandidates {
			return false
		}
		d.declared++
	}
	doc, err := d.f.Fetch(ctx, u, Conditional{})
	if err != nil || !doc.Kind.IsSource() {
		return false
	}
	return d.add(doc, title)
}

func (d *discovery) add(doc *Document, title string) bool {
	final, err := NormalizeURL(doc.FinalURL)
	if err != nil {
		return false
	}
	for _, c := range d.out {
		if c.URL == final {
			return false // reached through a redirect already recorded
		}
	}
	d.seen[final] = true
	d.out = append(d.out, Candidate{URL: final, Kind: doc.Kind, Title: firstNonEmpty(doc.Title, cleanText(title))})
	return true
}

// robotsSitemaps returns the Sitemap: URLs declared in the origin's
// robots.txt. Failures yield no URLs.
func (f *Fetcher) robotsSitemaps(ctx context.Context, origin *url.URL) []string {
	robotsURL := origin.ResolveReference(&url.URL{Path: "/robots.txt"})
	resp, err := f.get(ctx, robotsURL.String(), Conditional{}, robotsMaxBytes, "text/plain, */*;q=0.5")
	if err != nil || resp.notModified {
		return nil
	}
	return parseRobotsSitemaps(resp.body, robotsURL)
}

func parseRobotsSitemaps(body []byte, base *url.URL) []string {
	var out []string
	seen := make(map[string]bool)
	sc := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(body, utf8BOM)))
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	for sc.Scan() && len(out) < maxRobotsSitemaps {
		line, _, _ := strings.Cut(sc.Text(), "#")
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "sitemap") {
			continue
		}
		if u, ok := resolveRef(base, value); ok && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}
