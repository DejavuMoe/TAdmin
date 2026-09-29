package intel

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors. Use errors.Is to test them; *APIError matches the
// HTTP-status ones.
var (
	// ErrNoAPIKey is returned by NewTypeSafe without an API key.
	ErrNoAPIKey = errors.New("intel: typesafe api key is not configured")
	// ErrUnauthorized matches 401/403 responses (invalid or missing key).
	ErrUnauthorized = errors.New("intel: typesafe rejected the api key")
	// ErrRateLimited matches 429 Too Many Requests and 529 Overloaded; the
	// docs ask callers to back off before retrying.
	ErrRateLimited = errors.New("intel: typesafe rate limited or overloaded")
	// ErrInvalidRequest matches other 4xx responses (e.g. 422 validation).
	ErrInvalidRequest = errors.New("intel: typesafe rejected the request")
	// ErrServer matches other 5xx responses.
	ErrServer = errors.New("intel: typesafe server error")
	// ErrTimeout is returned when the per-call timeout elapses.
	ErrTimeout = errors.New("intel: typesafe request timed out")
	// ErrMalformedResponse is returned when a 2xx body does not match the
	// documented response shape.
	ErrMalformedResponse = errors.New("intel: malformed typesafe response")
)

// maxRetryAfter caps server-provided back-off hints.
const maxRetryAfter = time.Hour

// APIError is a non-2xx response from the TypeSafe API. The response body is
// intentionally not retained: validation errors may echo request content,
// which contains profile text.
type APIError struct {
	StatusCode int
	// RetryAfter is the server's requested wait from the retry-after-ms or
	// Retry-After header, or 0 when absent.
	RetryAfter time.Duration
	// RequestID is the x-typesafe-request-id response header, if any.
	RequestID string
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "intel: typesafe api returned HTTP %d", e.StatusCode)
	if e.RetryAfter > 0 {
		fmt.Fprintf(&b, " (retry after %s)", e.RetryAfter)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " [request id %s]", e.RequestID)
	}
	return b.String()
}

// Is maps the status code onto the sentinel errors.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
	case ErrRateLimited:
		return e.StatusCode == http.StatusTooManyRequests || e.StatusCode == 529
	case ErrServer:
		return e.StatusCode >= 500 && e.StatusCode != 529
	case ErrInvalidRequest:
		return e.StatusCode >= 400 && e.StatusCode < 500 &&
			e.StatusCode != http.StatusUnauthorized && e.StatusCode != http.StatusForbidden &&
			e.StatusCode != http.StatusTooManyRequests
	}
	return false
}

// Temporary reports whether retrying later may succeed (rate limit,
// overload, request timeout, or server error).
func (e *APIError) Temporary() bool {
	return e.StatusCode == http.StatusRequestTimeout || e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// RetryAfter extracts the server's back-off hint from err, if any.
func RetryAfter(err error) (time.Duration, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		return apiErr.RetryAfter, true
	}
	return 0, false
}

// parseRetryAfter reads retry-after-ms (milliseconds) or Retry-After
// (seconds or an HTTP date), the headers the TypeSafe SDKs honor.
func parseRetryAfter(h http.Header, now time.Time) time.Duration {
	var d time.Duration
	if v := strings.TrimSpace(h.Get("retry-after-ms")); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil && ms > 0 && !math.IsInf(ms, 0) {
			d = time.Duration(math.Min(ms, float64(maxRetryAfter/time.Millisecond)) * float64(time.Millisecond))
		}
	}
	if d == 0 {
		if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
			if s, err := strconv.ParseFloat(v, 64); err == nil {
				if s > 0 && !math.IsInf(s, 0) {
					d = time.Duration(math.Min(s, maxRetryAfter.Seconds()) * float64(time.Second))
				}
			} else if t, err := http.ParseTime(v); err == nil {
				d = t.Sub(now)
			}
		}
	}
	if d < 0 {
		return 0
	}
	return min(d, maxRetryAfter)
}
