package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestDecodeExtendedUpdateRetainsGuardQueryID(t *testing.T) {
	raw := []byte(`{
		"update_id":123,
		"chat_join_request":{
			"chat":{"id":-1001,"type":"supergroup","title":"Source"},
			"from":{"id":42,"is_bot":false,"first_name":"User"},
			"user_chat_id":900042,
			"date":1786276800,
			"query_id":"AAE-secret-query"
		}
	}`)
	update, err := decodeExtendedUpdate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if update.QueryID != "AAE-secret-query" || update.Update.ChatJoinRequest == nil || update.Update.ChatJoinRequest.From.ID != 42 {
		t.Fatalf("decoded update = %+v", update)
	}
}

func TestInviteSourceNeverKeepsFullInviteURL(t *testing.T) {
	link := &models.ChatInviteLink{Name: "https://t.me/+full-secret-link"}
	got := inviteSource(link)
	if strings.Contains(got, "t.me") || strings.Contains(got, "full-secret-link") {
		t.Fatalf("invite source = %q", got)
	}
}

func TestRawGuardMethodsSendOfficialParametersAndParseResult(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		mu.Lock()
		requests[method] = body
		mu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()

	api := &rawBotAPI{baseURL: server.URL, token: "123:test", version: "test", httpClient: server.Client()}
	if err := api.sendChatJoinRequestWebApp(context.Background(), "query-1", "https://verify.example/verify/token"); err != nil {
		t.Fatal(err)
	}
	if err := api.answerChatJoinRequestQuery(context.Background(), "query-2", "queue"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := requests["sendChatJoinRequestWebApp"]; got["chat_join_request_query_id"] != "query-1" || got["web_app_url"] != "https://verify.example/verify/token" {
		t.Fatalf("send params = %#v", got)
	}
	if got := requests["answerChatJoinRequestQuery"]; got["chat_join_request_query_id"] != "query-2" || got["result"] != "queue" {
		t.Fatalf("answer params = %#v", got)
	}
}

func TestRawGuardMethodRejectsFalseResultWithoutLeakingToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"ok":true,"result":false}`))
	}))
	defer server.Close()
	token := "123:do-not-leak"
	api := &rawBotAPI{baseURL: server.URL, token: token, version: "test", httpClient: server.Client()}
	err := api.answerChatJoinRequestQuery(context.Background(), "query", "approve")
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("error = %v", err)
	}
}
