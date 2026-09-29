package feeds

import (
	"context"
	"fmt"
)

// Walk limits.
const (
	// DefaultMaxWalkURLs is used when Walk is called with maxURLs <= 0.
	DefaultMaxWalkURLs = 50000
	// MaxWalkDepth is the deepest level of nested sitemap indexes followed;
	// the root document is depth 0.
	MaxWalkDepth = 3
	// MaxWalkFetches bounds the number of documents fetched by one Walk.
	MaxWalkFetches = 1000
)

// WalkResult is the outcome of Walk.
type WalkResult struct {
	// Kind is the kind of the root document.
	Kind Kind
	// Entries are the page URLs from all documents, deduplicated in
	// document order.
	Entries []Entry
	// Errors collects child fetch/parse failures and limit notices; they do
	// not abort the walk.
	Errors []error
	// Fetched counts successfully fetched documents, including the root.
	Fetched int
	// Truncated is set when maxURLs or MaxWalkFetches stopped the walk
	// before every entry was collected.
	Truncated bool
}

// Walk fetches a sitemap, sitemap index or feed and returns its page
// entries. Sitemap indexes are expanded recursively (up to MaxWalkDepth
// levels, each child fetched once) until maxURLs entries were collected.
// Failure to fetch the root is returned as an error; child failures are
// collected in WalkResult.Errors. If ctx is cancelled the partial result is
// returned together with ctx.Err().
func (f *Fetcher) Walk(ctx context.Context, rawURL string, maxURLs int) (*WalkResult, error) {
	if maxURLs <= 0 {
		maxURLs = DefaultMaxWalkURLs
	}
	doc, err := f.Fetch(ctx, rawURL, Conditional{})
	if err != nil {
		return nil, err
	}
	if !doc.Kind.IsSource() {
		return nil, fmt.Errorf("%w: %s is an HTML page, not a sitemap or feed", ErrUnsupported, doc.FinalURL)
	}
	w := &walker{
		f:       f,
		res:     &WalkResult{Kind: doc.Kind, Fetched: 1},
		set:     newEntrySet(maxURLs),
		visited: make(map[string]bool),
	}
	w.markVisited(rawURL)
	w.markVisited(doc.FinalURL)
	w.consume(ctx, doc, 0)
	w.res.Entries = w.set.items
	w.res.Truncated = w.res.Truncated || w.set.truncated
	if err := ctx.Err(); err != nil {
		return w.res, err
	}
	return w.res, nil
}

type walker struct {
	f       *Fetcher
	res     *WalkResult
	set     *entrySet
	visited map[string]bool
	limited bool
}

func (w *walker) markVisited(raw string) {
	if u, err := NormalizeURL(raw); err == nil {
		w.visited[u] = true
	}
}

func (w *walker) consume(ctx context.Context, doc *Document, depth int) {
	for _, e := range doc.Entries {
		if !w.set.add(e) {
			return
		}
	}
	if doc.Kind != KindSitemapIndex || len(doc.Children) == 0 {
		return
	}
	if depth+1 > MaxWalkDepth {
		w.res.Errors = append(w.res.Errors, fmt.Errorf("feeds: %s: sitemap index nesting exceeds depth %d; %d children skipped",
			doc.FinalURL, MaxWalkDepth, len(doc.Children)))
		return
	}
	for _, child := range doc.Children {
		if ctx.Err() != nil {
			return
		}
		if w.visited[child.URL] {
			continue
		}
		if w.set.full() {
			w.res.Truncated = true
			return
		}
		if w.res.Fetched >= MaxWalkFetches {
			if !w.limited {
				w.limited = true
				w.res.Truncated = true
				w.res.Errors = append(w.res.Errors, fmt.Errorf("feeds: stopped after fetching %d documents", MaxWalkFetches))
			}
			return
		}
		w.visited[child.URL] = true
		cdoc, err := w.f.Fetch(ctx, child.URL, Conditional{})
		if err != nil {
			w.res.Errors = append(w.res.Errors, fmt.Errorf("feeds: child sitemap %s: %w", child.URL, err))
			continue
		}
		w.res.Fetched++
		w.markVisited(cdoc.FinalURL)
		if !cdoc.Kind.IsSource() {
			w.res.Errors = append(w.res.Errors, fmt.Errorf("feeds: child sitemap %s: %w: got %s", child.URL, ErrUnsupported, cdoc.Kind))
			continue
		}
		w.consume(ctx, cdoc, depth+1)
	}
}
