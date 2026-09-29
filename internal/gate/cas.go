package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// CASChecker reports whether a user is listed by Combot Anti-Spam.
type CASChecker interface {
	Listed(ctx context.Context, userID int64) (bool, error)
}

// CAS queries https://api.cas.chat with a small positive/negative cache.
type CAS struct {
	BaseURL string
	HTTP    *http.Client

	mu    sync.Mutex
	cache map[int64]casEntry
}

type casEntry struct {
	listed bool
	at     time.Time
}

// NewCAS returns a CAS client.
func NewCAS() *CAS {
	return &CAS{BaseURL: "https://api.cas.chat", HTTP: &http.Client{Timeout: 4 * time.Second}, cache: map[int64]casEntry{}}
}

// Listed implements CASChecker.
func (c *CAS) Listed(ctx context.Context, userID int64) (bool, error) {
	c.mu.Lock()
	if e, ok := c.cache[userID]; ok && time.Since(e.at) < time.Hour {
		c.mu.Unlock()
		return e.listed, nil
	}
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/check?user_id="+strconv.FormatInt(userID, 10), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "TAdmin/2")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("cas: HTTP %d", resp.StatusCode)
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return false, fmt.Errorf("cas: %w", err)
	}
	c.mu.Lock()
	if len(c.cache) > 10000 {
		c.cache = map[int64]casEntry{}
	}
	c.cache[userID] = casEntry{listed: body.OK, at: time.Now()}
	c.mu.Unlock()
	return body.OK, nil
}
