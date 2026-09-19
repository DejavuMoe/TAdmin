package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	telegrambot "github.com/go-telegram/bot"

	"tadmin/internal/invite"
)

func TestInviteGatewayUsesJoinRequestLinksAndStandardMethods(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var createValues map[string]string
	var privateText string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		mu.Lock()
		methods = append(methods, method)
		if method == "createChatInviteLink" {
			createValues = map[string]string{
				"chat_id": request.FormValue("chat_id"), "name": request.FormValue("name"),
				"creates_join_request": request.FormValue("creates_join_request"),
				"member_limit":         request.FormValue("member_limit"),
			}
		}
		if method == "sendMessage" {
			privateText = request.FormValue("text")
		}
		mu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		switch method {
		case "sendMessage":
			_, _ = response.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":1,"type":"private"},"text":"ok"}}`))
		case "createChatInviteLink":
			_, _ = response.Write([]byte(`{"ok":true,"result":{"invite_link":"https://t.me/+gateway-secret","creator":{"id":123,"is_bot":true,"first_name":"TAdmin"},"creates_join_request":true,"is_primary":false,"is_revoked":false,"name":"TAdmin abcdefghijklmnop"}}`))
		case "revokeChatInviteLink":
			_, _ = response.Write([]byte(`{"ok":true,"result":{"invite_link":"https://t.me/+gateway-secret","creator":{"id":123,"is_bot":true,"first_name":"TAdmin"},"creates_join_request":true,"is_primary":false,"is_revoked":true}}`))
		default:
			http.Error(response, "unexpected method", http.StatusNotFound)
		}
	}))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{bot: bot, token: "123:test"}
	if err := gateway.SendInvitePreflight(context.Background(), 1, "preflight"); err != nil {
		t.Fatal(err)
	}
	link, err := gateway.CreateJoinRequestInvite(context.Background(), -1001, "TAdmin abcdefghijklmnop")
	if err != nil || link != (invite.TelegramLink{URL: "https://t.me/+gateway-secret", CreatesJoinRequest: true}) {
		t.Fatalf("CreateJoinRequestInvite() = %+v, %v", link, err)
	}
	if err := gateway.SendInviteSecret(context.Background(), 1, "secret follows", link.URL); err != nil {
		t.Fatal(err)
	}
	if err := gateway.RevokeInvite(context.Background(), -1001, link.URL); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if createValues["chat_id"] != "-1001" || createValues["name"] != "TAdmin abcdefghijklmnop" ||
		createValues["creates_join_request"] != "true" || createValues["member_limit"] != "" {
		t.Fatalf("create values = %#v", createValues)
	}
	if !strings.Contains(privateText, link.URL) {
		t.Fatalf("private delivery text = %q", privateText)
	}
	want := []string{"sendMessage", "createChatInviteLink", "sendMessage", "revokeChatInviteLink"}
	if fmt.Sprint(methods) != fmt.Sprint(want) {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
}

func TestInviteGatewayTreatsExpiredHashAsRevoked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: INVITE_HASH_EXPIRED"}`))
	}))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{bot: bot, token: "123:test"}
	if err := gateway.RevokeInvite(context.Background(), -1001, "https://t.me/+already-revoked"); err != nil {
		t.Fatalf("RevokeInvite() error = %v", err)
	}
}

func TestInviteGatewayClassifiesPermissionErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: not enough rights to invite users"}`))
	}))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{bot: bot, token: "123:test"}
	if _, err := gateway.CreateJoinRequestInvite(context.Background(), -1001, "TAdmin abcdefghijklmnop"); !errors.Is(err, invite.ErrTelegramPermission) {
		t.Fatalf("CreateJoinRequestInvite() error = %v", err)
	}
	if err := gateway.RevokeInvite(context.Background(), -1001, "https://t.me/+permission"); !errors.Is(err, invite.ErrTelegramPermission) {
		t.Fatalf("RevokeInvite() error = %v", err)
	}
}

func TestDirectDeliveryUsesPrivateAndURLFallback(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		values := map[string]string{
			"chat_id": request.FormValue("chat_id"), "receiver_user_id": request.FormValue("receiver_user_id"),
			"reply_markup": request.FormValue("reply_markup"),
		}
		mu.Lock()
		calls = append(calls, values)
		mu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		if values["chat_id"] == "42" {
			_, _ = response.Write([]byte(`{"ok":false,"error_code":403,"description":"bot was blocked"}`))
			return
		}
		_, _ = response.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":-1001,"type":"supergroup"},"text":"verify"}}`))
	}))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{bot: bot, token: "123:test"}
	deepLink := "https://t.me/TAdminTestBot?start=verify_token"
	if err := gateway.DeliverDirectVerification(context.Background(), -1001, 42, "verify", "https://verify.example/verify/token", deepLink); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("sendMessage calls = %d: %#v", len(calls), calls)
	}
	fallback := calls[1]["reply_markup"]
	if !strings.Contains(fallback, deepLink) || strings.Contains(fallback, "web_app") {
		t.Fatalf("group fallback markup = %s", fallback)
	}
}

func TestDirectDeliveryKeepsGroupFallbackUnusedWhenPrivateSucceeds(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		calls = append(calls, map[string]string{
			"chat_id": request.FormValue("chat_id"), "receiver_user_id": request.FormValue("receiver_user_id"),
			"reply_markup": request.FormValue("reply_markup"),
		})
		mu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":42,"type":"private"},"text":"verify"}}`))
	}))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{bot: bot, token: "123:test"}
	if err := gateway.DeliverDirectVerification(context.Background(), -1001, 42, "verify", "https://verify.example/verify/token", "https://t.me/TAdminTestBot?start=verify_token"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0]["chat_id"] != "42" || !strings.Contains(calls[0]["reply_markup"], "web_app") {
		t.Fatalf("delivery calls = %#v", calls)
	}
}

func TestRestorePreservesExplicitPriorPermissions(t *testing.T) {
	var restrictedPermissions string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		if method != "restrictChatMember" {
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
		}
		response.Header().Set("Content-Type", "application/json")
		switch method {
		case "getChat":
			_, _ = response.Write([]byte(`{"ok":true,"result":{"id":-1001,"type":"supergroup","title":"Source","permissions":{"can_send_messages":true,"can_send_photos":false,"can_invite_users":true,"can_pin_messages":true,"can_edit_tag":false,"can_react_to_messages":false}}}`))
		case "restrictChatMember":
			var body struct {
				Permissions json.RawMessage `json:"permissions"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			restrictedPermissions = string(body.Permissions)
			_, _ = response.Write([]byte(`{"ok":true,"result":true}`))
		default:
			http.Error(response, fmt.Sprintf("unexpected method %s", method), http.StatusNotFound)
		}
	}))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{bot: bot, token: "123:test", raw: &rawBotAPI{baseURL: server.URL, token: "123:test", httpClient: server.Client()}}
	if err := gateway.RestorePriorPermissions(context.Background(), -1001, 42, `{"can_send_messages":true,"can_send_photos":false,"can_pin_messages":true,"can_edit_tag":false,"can_react_to_messages":false}`, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restrictedPermissions, `"can_edit_tag":false`) || !strings.Contains(restrictedPermissions, `"can_react_to_messages":false`) || !strings.Contains(restrictedPermissions, `"can_send_messages":true`) || !strings.Contains(restrictedPermissions, `"can_send_photos":false`) {
		t.Fatalf("restored permissions = %s", restrictedPermissions)
	}
}

func TestModerationMuteUsesExplicitUntilDateAndFalsePermissions(t *testing.T) {
	var untilDate, permissions string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		untilDate = request.FormValue("until_date")
		permissions = request.FormValue("permissions")
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{bot: bot, token: "123:test"}
	until := time.Date(2026, 8, 9, 12, 10, 0, 0, time.UTC)
	if err := gateway.RestrictMemberUntil(context.Background(), -1001, 42, until); err != nil {
		t.Fatal(err)
	}
	if untilDate != fmt.Sprintf("%d", until.Unix()) || !strings.Contains(permissions, `"can_send_messages":false`) || strings.Contains(permissions, `:true`) {
		t.Fatalf("until_date=%q permissions=%q", untilDate, permissions)
	}
}

func TestKickUsesSingleUnbanAndBanIsExplicit(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		calls = append(calls, method)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{bot: bot, token: "123:test"}
	if err := gateway.KickMember(context.Background(), -1001, 42); err != nil {
		t.Fatal(err)
	}
	if err := gateway.BanMember(context.Background(), -1001, 43); err != nil {
		t.Fatal(err)
	}
	want := []string{"unbanChatMember", "banChatMember"}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestDeleteModerationMessageTreatsAlreadyDeletedAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message to delete not found"}`))
	}))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if err := (&gateway{bot: bot, token: "123:test"}).DeleteModerationMessage(context.Background(), -1001, 1); err != nil {
		t.Fatalf("already deleted message: %v", err)
	}
}

func TestMemberActionsRejectFalseAPISuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true,"result":false}`) }))
	defer server.Close()
	bot, err := telegrambot.New("123:test", telegrambot.WithSkipGetMe(), telegrambot.WithServerURL(server.URL), telegrambot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{bot: bot, token: "123:test"}
	for name, action := range map[string]func(context.Context, int64, int64) error{"restrict": gateway.RestrictMember, "kick": gateway.KickMember, "ban": gateway.BanMember, "decline": gateway.DeclineJoinRequest, "approve": gateway.ApproveJoinRequest} {
		if err := action(context.Background(), -1001, 42); err == nil {
			t.Errorf("%s accepted false result", name)
		}
	}
}

func TestNormalRestoreLiftsRestrictionsAndShortPriorExpiryWaits(t *testing.T) {
	var calls int
	var rights map[string]bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Permissions map[string]bool `json:"permissions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		rights = body.Permissions
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	}))
	defer server.Close()
	gateway := &gateway{raw: &rawBotAPI{baseURL: server.URL, token: "123:test", httpClient: server.Client()}}
	if err := gateway.RestoreDefaultPermissions(context.Background(), -1001, 42); err != nil {
		t.Fatal(err)
	}
	if len(rights) != 16 {
		t.Fatalf("permission fields=%d", len(rights))
	}
	for field, allowed := range rights {
		if !allowed {
			t.Errorf("%s is still individually restricted", field)
		}
	}
	if err := gateway.RestorePriorPermissions(context.Background(), -1001, 42, `{}`, time.Now().Unix()+10); err == nil {
		t.Fatal("short until_date would create an unintended permanent restriction")
	}
	if calls != 1 {
		t.Fatal("sent unsafe short restriction")
	}
	if err := gateway.RestorePriorPermissions(context.Background(), -1001, 42, `{}`, time.Now().Unix()-1); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("expired prior restriction was not lifted")
	}
}
