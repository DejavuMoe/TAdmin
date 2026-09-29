package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public Bot API endpoint.
const DefaultBaseURL = "https://api.telegram.org"

// Error is a Bot API error response.
type Error struct {
	Method      string
	Code        int
	Description string
	RetryAfter  time.Duration
	MigrateTo   int64
}

func (e *Error) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// AsError extracts a *Error from err.
func AsError(err error) (*Error, bool) {
	var apiErr *Error
	ok := errors.As(err, &apiErr)
	return apiErr, ok
}

func descContains(err error, parts ...string) bool {
	apiErr, ok := AsError(err)
	if !ok {
		return false
	}
	d := strings.ToLower(apiErr.Description)
	for _, p := range parts {
		if strings.Contains(d, p) {
			return true
		}
	}
	return false
}

// IsForbidden reports 403 errors (blocked by user, kicked from chat...).
func IsForbidden(err error) bool {
	apiErr, ok := AsError(err)
	return ok && apiErr.Code == http.StatusForbidden
}

// IsTooManyRequests reports a 429 error.
func IsTooManyRequests(err error) bool {
	apiErr, ok := AsError(err)
	return ok && apiErr.Code == http.StatusTooManyRequests
}

// IsGone reports errors meaning the target message/user/chat no longer
// exists, so retrying is pointless and the step can be considered done.
func IsGone(err error) bool {
	return descContains(err,
		"message to delete not found", "message to edit not found", "message can't be deleted",
		"message not found", "message_id_invalid", "message to unpin not found",
		"user not found", "participant_id_invalid", "user_not_participant", "hide_requester_missing",
		"chat not found", "peer_id_invalid", "user_already_participant", "message is not modified",
		"ephemeral message not found", "query is too old", "query id is invalid", "request_query_invalid")
}

// IsNotModified reports "message is not modified".
func IsNotModified(err error) bool { return descContains(err, "message is not modified") }

// IsPermanent reports client errors that will not succeed on retry.
func IsPermanent(err error) bool {
	apiErr, ok := AsError(err)
	if !ok {
		return false
	}
	return apiErr.Code == 400 || apiErr.Code == 403 || apiErr.Code == 404
}

// Upload is a file sent with multipart/form-data.
type Upload struct {
	Field string // form field name; reference it as "attach://<Field>"
	Name  string // file name
	Data  []byte
}

// Client is a Bot API client. It is safe for concurrent use.
type Client struct {
	token   string
	baseURL string
	http    *http.Client

	global *bucket
	mu     sync.Mutex
	chats  map[int64]*bucket
	pauses map[int64]time.Time
}

// Option customizes a Client.
type Option func(*Client)

// WithBaseURL points the client at a different Bot API server (tests).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New creates a client for token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: DefaultBaseURL,
		http:    &http.Client{Timeout: 75 * time.Second},
		global:  newBucket(25, 30, time.Second),
		chats:   make(map[int64]*bucket),
		pauses:  make(map[int64]time.Time),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// sendMethods are throttled per chat. Other calls only use the global bucket,
// so moderation actions are never queued behind chat messages.
var sendMethods = map[string]bool{
	"sendMessage": true, "sendPhoto": true, "sendDocument": true, "sendAnimation": true,
	"copyMessage": true, "forwardMessage": true,
}

func (c *Client) chatBucket(chatID int64) *bucket {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.chats[chatID]
	if !ok {
		if chatID < 0 {
			// Groups: Telegram documents ~20 messages per minute.
			b = newBucket(5, 1, 3*time.Second)
		} else {
			b = newBucket(3, 1, time.Second)
		}
		if len(c.chats) > 4096 {
			c.chats = make(map[int64]*bucket)
		}
		c.chats[chatID] = b
	}
	return b
}

func (c *Client) waitPause(ctx context.Context, chatID int64) error {
	c.mu.Lock()
	until := c.pauses[chatID]
	c.mu.Unlock()
	if d := time.Until(until); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func (c *Client) setPause(chatID int64, d time.Duration) {
	c.mu.Lock()
	c.pauses[chatID] = time.Now().Add(d)
	c.mu.Unlock()
}

// timedMethods carry an absolute until_date computed by the caller. Retrying a
// 429 here would send a stale deadline (Telegram treats < 30s as forever), so
// the error is returned and the caller recomputes.
var timedMethods = map[string]bool{"banChatMember": true, "restrictChatMember": true}

// Call invokes method with JSON params and decodes the result into out
// (which may be nil). Throttling and short 429 back-off are applied.
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	return c.call(ctx, method, params, nil, out)
}

// CallUpload invokes method with multipart params and file uploads.
func (c *Client) CallUpload(ctx context.Context, method string, params any, uploads []Upload, out any) error {
	return c.call(ctx, method, params, uploads, out)
}

func (c *Client) call(ctx context.Context, method string, params any, uploads []Upload, out any) error {
	chatID := chatIDOf(params)
	throttled := sendMethods[method] && chatID != 0
	for attempt := 0; ; attempt++ {
		if throttled {
			if err := c.waitPause(ctx, chatID); err != nil {
				return err
			}
			if err := c.chatBucket(chatID).wait(ctx); err != nil {
				return err
			}
		}
		if err := c.global.wait(ctx); err != nil {
			return err
		}
		err := c.do(ctx, method, params, uploads, out)
		apiErr, ok := AsError(err)
		if !ok || apiErr.Code != http.StatusTooManyRequests || attempt >= 2 || timedMethods[method] {
			return err
		}
		wait := apiErr.RetryAfter
		if wait <= 0 {
			wait = time.Second
		}
		if throttled {
			c.setPause(chatID, wait)
		}
		if wait > 30*time.Second {
			return err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter      int   `json:"retry_after"`
		MigrateToChatID int64 `json:"migrate_to_chat_id"`
	} `json:"parameters"`
}

func (c *Client) do(ctx context.Context, method string, params any, uploads []Upload, out any) error {
	var body io.Reader
	contentType := "application/json"
	if len(uploads) == 0 {
		if params == nil {
			params = struct{}{}
		}
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("telegram %s: encode params: %w", method, err)
		}
		body = bytes.NewReader(data)
	} else {
		buf, ct, err := encodeMultipart(params, uploads)
		if err != nil {
			return fmt.Errorf("telegram %s: encode multipart: %w", method, err)
		}
		body, contentType = buf, ct
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, body)
	if err != nil {
		return fmt.Errorf("telegram %s: %s", method, c.redact(err.Error()))
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Never leak the token embedded in the request URL.
		return fmt.Errorf("telegram %s: %s", method, c.redact(err.Error()))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("telegram %s: read response: %s", method, c.redact(err.Error()))
	}
	var r apiResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return &Error{Method: method, Code: resp.StatusCode, Description: "invalid response: " + resp.Status}
	}
	if !r.OK {
		e := &Error{Method: method, Code: r.ErrorCode, Description: r.Description}
		if r.Parameters != nil {
			e.RetryAfter = time.Duration(r.Parameters.RetryAfter) * time.Second
			e.MigrateTo = r.Parameters.MigrateToChatID
		}
		if e.Code == 0 {
			e.Code = resp.StatusCode
		}
		return e
	}
	if out != nil {
		if err := json.Unmarshal(r.Result, out); err != nil {
			return fmt.Errorf("telegram %s: decode result: %w", method, err)
		}
	}
	return nil
}

func (c *Client) redact(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "<token>")
}

// encodeMultipart flattens params (a JSON-marshalable struct or map) into
// form fields: strings are sent raw, everything else as JSON.
func encodeMultipart(params any, uploads []Upload) (*bytes.Buffer, string, error) {
	fields := map[string]json.RawMessage{}
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, "", err
		}
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, "", err
		}
	}
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	for k, raw := range fields {
		value := string(raw)
		if len(raw) > 0 && raw[0] == '"' {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, "", err
			}
			value = s
		}
		if err := w.WriteField(k, value); err != nil {
			return nil, "", err
		}
	}
	for _, u := range uploads {
		fw, err := w.CreateFormFile(u.Field, u.Name)
		if err != nil {
			return nil, "", err
		}
		if _, err := fw.Write(u.Data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf, w.FormDataContentType(), nil
}

// chatIDOf extracts a numeric chat_id from params for throttling.
func chatIDOf(params any) int64 {
	if p, ok := params.(interface{ throttleChat() int64 }); ok {
		return p.throttleChat()
	}
	return 0
}

// bucket is a small token bucket.
type bucket struct {
	mu       sync.Mutex
	tokens   float64
	capacity float64
	rate     float64 // tokens per second
	last     time.Time
}

func newBucket(capacity int, refill int, per time.Duration) *bucket {
	return &bucket{
		tokens:   float64(capacity),
		capacity: float64(capacity),
		rate:     float64(refill) / per.Seconds(),
		last:     time.Now(),
	}
}

func (b *bucket) wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		now := time.Now()
		b.tokens += now.Sub(b.last).Seconds() * b.rate
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		need := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		t := time.NewTimer(need)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
