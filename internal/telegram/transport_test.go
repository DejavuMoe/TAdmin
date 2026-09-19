package telegram

import (
	"context"
	"errors"
	"fmt"
	telegrambot "github.com/go-telegram/bot"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransportHonorsFloodWaitAndCancellation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(429)
		fmt.Fprint(w, `{"ok":false,"parameters":{"retry_after":30}}`)
	}))
	defer server.Close()
	client := newHTTPClient()
	defer client.CloseIdleConnections()
	r, err := client.Post(server.URL+"/sendMessage", "application/json", strings.NewReader(`{"chat_id":-1001}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/getUpdates", nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("ignored global Telegram flood wait")
	}
	if requests.Load() != 1 {
		t.Fatal("sent a request during flood wait")
	}
}
func TestRawErrorRedactsToken(t *testing.T) {
	const token = "123:synthetic-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"ok":false,"error_code":400,"description":"%s"}`, token)
	}))
	defer server.Close()
	api := rawBotAPI{baseURL: server.URL, token: token, httpClient: server.Client()}
	err := api.call(context.Background(), "getUpdates", nil, nil)
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestSDKStreamingMultipartMessagesShareGroupLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		if r.FormValue("chat_id") != "-1001" {
			t.Error("streamed multipart body changed")
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":-1001,"type":"supergroup"}}}`)
	}))
	defer server.Close()
	client := newHTTPClient()
	defer client.CloseIdleConnections()
	bot, err := telegrambot.New("123:offline", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, client))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bot.SendMessage(context.Background(), &telegrambot.SendMessageParams{ChatID: -1001, Text: "one"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := bot.SendMessage(ctx, &telegrambot.SendMessageParams{ChatID: -1001, Text: "two"}); err == nil {
		t.Fatal("SDK multipart bypassed group send limit")
	}
	if calls.Load() != 1 {
		t.Fatal("second request was transmitted before group interval")
	}
}

func TestSDKDecodeFailureDoesNotLogRawPrivateResponse(t *testing.T) {
	err := errors.New("error decode response body for method sendMessage, secret-verification-link, invalid JSON")
	if got := sanitizeError(err, "123:token"); strings.Contains(got, "secret-verification-link") || strings.Contains(got, "invalid JSON") {
		t.Fatalf("raw response leaked: %s", got)
	}
}
