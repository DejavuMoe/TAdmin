// Package tgtest provides an in-memory fake of the Telegram Bot API for
// end-to-end tests: it records every call and keeps just enough state
// (members, messages, pins) to exercise TAdmin's flows.
package tgtest

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tadmin/internal/tg"
)

// Call is one recorded API call.
type Call struct {
	Method string
	Params map[string]any
}

// Int returns a numeric param.
func (c Call) Int(key string) int64 {
	switch v := c.Params[key].(type) {
	case float64:
		return int64(v)
	case string:
		var n json.Number = json.Number(v)
		i, _ := n.Int64()
		return i
	}
	return 0
}

// Str returns a string param.
func (c Call) Str(key string) string {
	if v, ok := c.Params[key].(string); ok {
		return v
	}
	if v, ok := c.Params[key]; ok && v != nil {
		b, _ := json.Marshal(v)
		return string(b)
	}
	return ""
}

// Has reports whether the param is present.
func (c Call) Has(key string) bool { _, ok := c.Params[key]; return ok }

// Obj returns an object param (decoding JSON strings from multipart).
func (c Call) Obj(key string) map[string]any {
	switch v := c.Params[key].(type) {
	case map[string]any:
		return v
	case string:
		var m map[string]any
		_ = json.Unmarshal([]byte(v), &m)
		return m
	}
	return nil
}

// Fault forces an error response.
type Fault struct {
	Code        int
	Description string
	Times       int // 0 = forever
}

// Server is the fake Bot API.
type Server struct {
	*httptest.Server
	Bot tg.User

	mu      sync.Mutex
	calls   []Call
	members map[[2]int64]tg.ChatMember
	admins  map[int64][]tg.ChatMember
	chats   map[int64]tg.ChatFullInfo
	faults  map[string]*Fault
	msgID   int
	ephID   int
	pinned  map[int64][]*tg.Message
	updates []tg.Update
	nextUpd int64
}

// Push queues updates for getUpdates (ids are assigned automatically).
func (s *Server) Push(ups ...tg.Update) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range ups {
		s.nextUpd++
		u.UpdateID = s.nextUpd
		s.updates = append(s.updates, u)
	}
}

// New starts a fake server.
func New(t testing.TB) *Server {
	s := &Server{
		Bot:     tg.User{ID: 999, IsBot: true, FirstName: "TAdmin", Username: "tadmin_test_bot"},
		members: map[[2]int64]tg.ChatMember{},
		admins:  map[int64][]tg.ChatMember{},
		chats:   map[int64]tg.ChatFullInfo{},
		faults:  map[string]*Fault{},
		msgID:   100,
		ephID:   500,
		pinned:  map[int64][]*tg.Message{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

// Client returns a tg client bound to the fake server.
func (s *Server) Client() *tg.Client { return tg.New("123:TEST", tg.WithBaseURL(s.URL)) }

// SetMember sets a member's state.
func (s *Server) SetMember(chatID int64, m tg.ChatMember) {
	s.mu.Lock()
	s.members[[2]int64{chatID, m.User.ID}] = m
	s.mu.Unlock()
}

// Member returns the current member state.
func (s *Server) Member(chatID, userID int64) tg.ChatMember {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.members[[2]int64{chatID, userID}]
	if !ok {
		return tg.ChatMember{Status: tg.StatusLeft, User: tg.User{ID: userID}}
	}
	return m
}

// SetAdmins sets the administrator list (the bot is added automatically).
func (s *Server) SetAdmins(chatID int64, ids ...int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := []tg.ChatMember{{Status: tg.StatusAdministrator, User: s.Bot, CanRestrictMembers: true, CanDeleteMessages: true}}
	for i, id := range ids {
		st := tg.StatusAdministrator
		if i == 0 {
			st = tg.StatusCreator
		}
		list = append(list, tg.ChatMember{Status: st, User: tg.User{ID: id, FirstName: "Admin"}, CanRestrictMembers: true})
	}
	s.admins[chatID] = list
}

// SetChat sets getChat info.
func (s *Server) SetChat(c tg.ChatFullInfo) {
	s.mu.Lock()
	s.chats[c.ID] = c
	s.mu.Unlock()
}

// Pin records a pinned message (newest last).
func (s *Server) Pin(chatID int64, m *tg.Message) {
	s.mu.Lock()
	s.pinned[chatID] = append(s.pinned[chatID], m)
	s.mu.Unlock()
}

// Pinned returns currently pinned message ids.
func (s *Server) Pinned(chatID int64) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for _, m := range s.pinned[chatID] {
		out = append(out, m.MessageID)
	}
	return out
}

// Fail makes method return an error.
func (s *Server) Fail(method string, f Fault) {
	s.mu.Lock()
	s.faults[method] = &f
	s.mu.Unlock()
}

// Calls returns recorded calls, optionally filtered by method.
func (s *Server) Calls(methods ...string) []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Call
	for _, c := range s.calls {
		if len(methods) == 0 {
			out = append(out, c)
			continue
		}
		for _, m := range methods {
			if c.Method == m {
				out = append(out, c)
			}
		}
	}
	return out
}

// Last returns the last call of method.
func (s *Server) Last(method string) (Call, bool) {
	cs := s.Calls(method)
	if len(cs) == 0 {
		return Call{}, false
	}
	return cs[len(cs)-1], true
}

// Reset clears recorded calls.
func (s *Server) Reset() {
	s.mu.Lock()
	s.calls = nil
	s.mu.Unlock()
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	method := parts[len(parts)-1]
	params := map[string]any{}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct == "multipart/form-data" {
		_ = r.ParseMultipartForm(8 << 20)
		for k, v := range r.MultipartForm.Value {
			var decoded any
			if json.Unmarshal([]byte(v[0]), &decoded) == nil {
				if _, isNum := decoded.(float64); isNum || isObj(decoded) {
					params[k] = decoded
					continue
				}
			}
			params[k] = v[0]
		}
		for k := range r.MultipartForm.File {
			params[k] = "<file>"
		}
	} else {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &params)
	}
	call := Call{Method: method, Params: params}
	s.mu.Lock()
	s.calls = append(s.calls, call)
	if f, ok := s.faults[method]; ok {
		if f.Times > 0 {
			f.Times--
			if f.Times == 0 {
				delete(s.faults, method)
			}
		}
		s.mu.Unlock()
		writeJSON(w, map[string]any{"ok": false, "error_code": f.Code, "description": f.Description})
		return
	}
	result := s.respond(call)
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "result": result})
}

func isObj(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// respond computes the result with s.mu held.
func (s *Server) respond(c Call) any {
	chatID := c.Int("chat_id")
	userID := c.Int("user_id")
	switch c.Method {
	case "getMe":
		return s.Bot
	case "getChat":
		if ch, ok := s.chats[chatID]; ok {
			if p := s.pinned[chatID]; len(p) > 0 {
				ch.PinnedMessage = p[len(p)-1]
			}
			return ch
		}
		ch := tg.ChatFullInfo{ID: chatID, Type: tg.ChatSupergroup, Title: "Test Group"}
		if chatID > 0 {
			ch.Type, ch.FirstName = tg.ChatPrivate, "User"
		}
		if p := s.pinned[chatID]; len(p) > 0 {
			ch.PinnedMessage = p[len(p)-1]
		}
		return ch
	case "getChatMember":
		if userID == s.Bot.ID {
			return tg.ChatMember{Status: tg.StatusAdministrator, User: s.Bot, CanRestrictMembers: true, CanDeleteMessages: true,
				ChatPermissions: tg.ChatPermissions{CanPinMessages: tg.Bool(true), CanInviteUsers: tg.Bool(true)}}
		}
		if m, ok := s.members[[2]int64{chatID, userID}]; ok {
			return m
		}
		return tg.ChatMember{Status: tg.StatusLeft, User: tg.User{ID: userID}}
	case "getChatAdministrators":
		return s.admins[chatID]
	case "sendMessage", "sendPhoto", "sendDocument":
		msg := tg.Message{Chat: tg.Chat{ID: chatID, Type: chatType(chatID)}, Text: c.Str("text"), Caption: c.Str("caption")}
		if eph := c.Obj("ephemeral_message_parameters"); eph != nil {
			s.ephID++
			msg.EphemeralMessageID = s.ephID
		} else {
			s.msgID++
			msg.MessageID = s.msgID
		}
		return msg
	case "restrictChatMember":
		key := [2]int64{chatID, userID}
		m := s.members[key]
		m.User.ID = userID
		perms := c.Obj("permissions")
		all := true
		for _, v := range perms {
			if b, ok := v.(bool); ok && !b {
				all = false
			}
		}
		if all && len(perms) > 0 {
			m.Status, m.ChatPermissions = tg.StatusMember, tg.ChatPermissions{}
		} else {
			m.Status, m.IsMember = tg.StatusRestricted, true
			b, _ := json.Marshal(perms)
			m.ChatPermissions = tg.ChatPermissions{}
			_ = json.Unmarshal(b, &m.ChatPermissions)
			m.UntilDate = c.Int("until_date")
		}
		s.members[key] = m
		return true
	case "banChatMember":
		key := [2]int64{chatID, userID}
		m := s.members[key]
		m.User.ID, m.Status, m.UntilDate = userID, tg.StatusKicked, c.Int("until_date")
		s.members[key] = m
		return true
	case "unbanChatMember":
		key := [2]int64{chatID, userID}
		if m, ok := s.members[key]; ok && m.Status == tg.StatusKicked {
			m.Status = tg.StatusLeft
			s.members[key] = m
		}
		return true
	case "approveChatJoinRequest":
		key := [2]int64{chatID, userID}
		s.members[key] = tg.ChatMember{Status: tg.StatusMember, User: tg.User{ID: userID}}
		return true
	case "unpinChatMessage":
		id := int(c.Int("message_id"))
		list := s.pinned[chatID]
		for i, m := range list {
			if id == 0 && i == len(list)-1 || m.MessageID == id {
				s.pinned[chatID] = append(list[:i:i], list[i+1:]...)
				break
			}
		}
		return true
	case "unpinAllChatMessages":
		delete(s.pinned, chatID)
		return true
	case "getChatMemberCount":
		return 42
	case "getWebhookInfo":
		return tg.WebhookInfo{}
	case "getUpdates":
		offset := c.Int("offset")
		var out []tg.Update
		for _, u := range s.updates {
			if u.UpdateID >= offset {
				out = append(out, u)
			}
		}
		if len(out) == 0 {
			s.mu.Unlock()
			time.Sleep(50 * time.Millisecond) // emulate a (very short) long poll
			s.mu.Lock()
		}
		return out
	}
	return true
}

func chatType(id int64) string {
	if id > 0 {
		return tg.ChatPrivate
	}
	return tg.ChatSupergroup
}
