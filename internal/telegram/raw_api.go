package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type rawBotAPI struct {
	baseURL    string
	token      string
	version    string
	httpClient *http.Client
}

func (a *rawBotAPI) call(ctx context.Context, method string, params, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode Telegram %s request: %w", method, err)
	}
	requestURL := a.baseURL + "/bot" + a.token + "/" + method
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Telegram %s request: %s", method, sanitizeError(err, a.token))
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "TAdmin/"+a.version)
	response, err := a.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call Telegram %s: %s", method, sanitizeError(err, a.token))
	}
	defer response.Body.Close()
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode Telegram %s response: %w", method, err)
	}
	if response.StatusCode != http.StatusOK || !envelope.OK {
		return fmt.Errorf("Telegram %s failed with code %d: %s", method, envelope.ErrorCode, strings.ReplaceAll(envelope.Description, a.token, "[REDACTED]"))
	}
	if result != nil {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("decode Telegram %s result: %w", method, err)
		}
	}
	return nil
}

func (a *rawBotAPI) sendChatJoinRequestWebApp(ctx context.Context, queryID, webAppURL string) error {
	var result bool
	err := a.call(ctx, "sendChatJoinRequestWebApp", struct {
		QueryID   string `json:"chat_join_request_query_id"`
		WebAppURL string `json:"web_app_url"`
	}{QueryID: queryID, WebAppURL: webAppURL}, &result)
	if err == nil && !result {
		return fmt.Errorf("Telegram sendChatJoinRequestWebApp returned false")
	}
	return err
}

func (a *rawBotAPI) answerChatJoinRequestQuery(ctx context.Context, queryID, resultValue string) error {
	var result bool
	err := a.call(ctx, "answerChatJoinRequestQuery", struct {
		QueryID string `json:"chat_join_request_query_id"`
		Result  string `json:"result"`
	}{QueryID: queryID, Result: resultValue}, &result)
	if err == nil && !result {
		return fmt.Errorf("Telegram answerChatJoinRequestQuery returned false")
	}
	return err
}
