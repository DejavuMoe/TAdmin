package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// All adapters share flood-control state. Never retry a potentially delivered
// send inside the transport; durable workflows decide how to recover ambiguity.
type limitedTransport struct {
	base         *http.Transport
	mu           sync.Mutex
	blockedUntil time.Time
	sends        map[string]time.Time
}

func (t *limitedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
	chatID := ""
	// The SDK streams multipart requests through io.Pipe, so GetBody is nil.
	// Buffer only the two small text methods we rate-limit, never file uploads.
	if (method == "sendMessage" || method == "editMessageText") && request.GetBody == nil && request.Body != nil {
		original := request.Body
		stop := context.AfterFunc(request.Context(), func() { _ = original.Close() })
		body, err := io.ReadAll(io.LimitReader(original, (1<<20)+1))
		stop()
		_ = original.Close()
		if err != nil {
			return nil, err
		}
		if len(body) > 1<<20 {
			return nil, errors.New("Telegram text request exceeds size limit")
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	if (method == "sendMessage" || method == "editMessageText") && request.GetBody != nil {
		body, err := request.GetBody()
		if err != nil {
			return nil, err
		}
		copy := request.Clone(request.Context())
		copy.Body = body
		if strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/form-data") {
			if err := copy.ParseMultipartForm(1 << 20); err == nil {
				chatID = copy.FormValue("chat_id")
			}
			if copy.MultipartForm != nil {
				_ = copy.MultipartForm.RemoveAll()
			}
		} else {
			var v struct {
				ChatID json.RawMessage `json:"chat_id"`
			}
			if json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&v) == nil {
				chatID = strings.Trim(string(v.ChatID), `"`)
			}
		}
		body.Close()
	}
	for {
		t.mu.Lock()
		now := time.Now()
		until := t.blockedUntil
		if chatID != "" && t.sends[chatID].After(until) {
			until = t.sends[chatID]
		}
		wait := time.Until(until)
		if wait <= 0 {
			if chatID != "" {
				if t.sends == nil {
					t.sends = make(map[string]time.Time)
				}
				for key, deadline := range t.sends {
					if deadline.Before(now) {
						delete(t.sends, key)
					}
				}
				interval := time.Second
				if strings.HasPrefix(chatID, "-") {
					interval = 3100 * time.Millisecond
				}
				t.sends[chatID] = now.Add(interval)
			}
			t.mu.Unlock()
			break
		}
		t.mu.Unlock()
		if err := waitContext(request.Context(), wait); err != nil {
			return nil, err
		}
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusTooManyRequests {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		var envelope struct {
			Parameters struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		_ = json.Unmarshal(body, &envelope)
		seconds := envelope.Parameters.RetryAfter
		if seconds < 1 {
			seconds = 1
		}
		if seconds > 86400 {
			seconds = 86400
		}
		t.mu.Lock()
		deadline := time.Now().Add(time.Duration(seconds) * time.Second)
		if deadline.After(t.blockedUntil) {
			t.blockedUntil = deadline
		}
		t.mu.Unlock()
	}
	return response, nil
}
func (t *limitedTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }
func waitContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
