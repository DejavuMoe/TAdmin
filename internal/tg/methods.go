package tg

import (
	"context"
	"strconv"
	"time"
)

// SendMessageParams are sendMessage parameters.
type SendMessageParams struct {
	ChatID             int64               `json:"chat_id"`
	Text               string              `json:"text"`
	ParseMode          string              `json:"parse_mode,omitempty"`
	LinkPreviewOptions *LinkPreviewOptions `json:"link_preview_options,omitempty"`
	DisableNotif       bool                `json:"disable_notification,omitempty"`
	ReplyParameters    *ReplyParameters    `json:"reply_parameters,omitempty"`
	ReplyMarkup        *InlineKeyboard     `json:"reply_markup,omitempty"`
	Ephemeral          *EphemeralParams    `json:"ephemeral_message_parameters,omitempty"`
}

func (p SendMessageParams) throttleChat() int64 {
	// Ephemeral messages are delivered to one user; do not spend the
	// group's shared message budget on them.
	if p.Ephemeral != nil {
		return 0
	}
	return p.ChatID
}

// SendMessage sends an HTML text message unless ParseMode is set.
func (c *Client) SendMessage(ctx context.Context, p SendMessageParams) (*Message, error) {
	if p.ParseMode == "" {
		p.ParseMode = "HTML"
	}
	if p.LinkPreviewOptions == nil {
		p.LinkPreviewOptions = &LinkPreviewOptions{IsDisabled: true}
	}
	var m Message
	return &m, c.Call(ctx, "sendMessage", p, &m)
}

// SendPhotoParams are sendPhoto parameters; the photo is uploaded.
type SendPhotoParams struct {
	ChatID          int64            `json:"chat_id"`
	Caption         string           `json:"caption,omitempty"`
	ParseMode       string           `json:"parse_mode,omitempty"`
	DisableNotif    bool             `json:"disable_notification,omitempty"`
	ReplyParameters *ReplyParameters `json:"reply_parameters,omitempty"`
	ReplyMarkup     *InlineKeyboard  `json:"reply_markup,omitempty"`
	Ephemeral       *EphemeralParams `json:"ephemeral_message_parameters,omitempty"`
	HasSpoiler      bool             `json:"has_spoiler,omitempty"`
}

func (p SendPhotoParams) throttleChat() int64 {
	if p.Ephemeral != nil {
		return 0
	}
	return p.ChatID
}

// SendPhoto uploads png as a photo.
func (c *Client) SendPhoto(ctx context.Context, p SendPhotoParams, png []byte) (*Message, error) {
	if p.ParseMode == "" {
		p.ParseMode = "HTML"
	}
	var m Message
	return &m, c.CallUpload(ctx, "sendPhoto", p, []Upload{{Field: "photo", Name: "challenge.png", Data: png}}, &m)
}

// SendDocumentParams are sendDocument parameters.
type SendDocumentParams struct {
	ChatID    int64  `json:"chat_id"`
	Caption   string `json:"caption,omitempty"`
	ParseMode string `json:"parse_mode,omitempty"`
}

func (p SendDocumentParams) throttleChat() int64 { return p.ChatID }

// SendDocument uploads a file.
func (c *Client) SendDocument(ctx context.Context, p SendDocumentParams, name string, data []byte) (*Message, error) {
	if p.ParseMode == "" {
		p.ParseMode = "HTML"
	}
	var m Message
	return &m, c.CallUpload(ctx, "sendDocument", p, []Upload{{Field: "document", Name: name, Data: data}}, &m)
}

// EditMessageTextParams are editMessageText parameters.
type EditMessageTextParams struct {
	ChatID             int64               `json:"chat_id"`
	MessageID          int                 `json:"message_id"`
	Text               string              `json:"text"`
	ParseMode          string              `json:"parse_mode,omitempty"`
	LinkPreviewOptions *LinkPreviewOptions `json:"link_preview_options,omitempty"`
	ReplyMarkup        *InlineKeyboard     `json:"reply_markup,omitempty"`
}

// EditMessageText edits a text message; "not modified" is not an error.
func (c *Client) EditMessageText(ctx context.Context, p EditMessageTextParams) error {
	if p.ParseMode == "" {
		p.ParseMode = "HTML"
	}
	if p.LinkPreviewOptions == nil {
		p.LinkPreviewOptions = &LinkPreviewOptions{IsDisabled: true}
	}
	err := c.Call(ctx, "editMessageText", p, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

// EditMessageCaption edits a media caption and keyboard.
func (c *Client) EditMessageCaption(ctx context.Context, chatID int64, messageID int, caption string, kb *InlineKeyboard) error {
	err := c.Call(ctx, "editMessageCaption", map[string]any{
		"chat_id": chatID, "message_id": messageID, "caption": caption, "parse_mode": "HTML", "reply_markup": kb,
	}, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

type inputMediaPhoto struct {
	Type      string `json:"type"`
	Media     string `json:"media"`
	Caption   string `json:"caption,omitempty"`
	ParseMode string `json:"parse_mode,omitempty"`
}

// EditMessagePhoto replaces the photo, caption and keyboard of a message.
func (c *Client) EditMessagePhoto(ctx context.Context, chatID int64, messageID int, png []byte, caption string, kb *InlineKeyboard) error {
	params := map[string]any{
		"chat_id": chatID, "message_id": messageID,
		"media":        inputMediaPhoto{Type: "photo", Media: "attach://p", Caption: caption, ParseMode: "HTML"},
		"reply_markup": kb,
	}
	return c.CallUpload(ctx, "editMessageMedia", params, []Upload{{Field: "p", Name: "challenge.png", Data: png}}, nil)
}

// EditMessageReplyMarkup replaces the inline keyboard.
func (c *Client) EditMessageReplyMarkup(ctx context.Context, chatID int64, messageID int, kb *InlineKeyboard) error {
	err := c.Call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": chatID, "message_id": messageID, "reply_markup": kb}, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

// EditEphemeralText edits an ephemeral text message.
func (c *Client) EditEphemeralText(ctx context.Context, chatID, userID int64, ephemeralID int, text string, kb *InlineKeyboard) error {
	err := c.Call(ctx, "editEphemeralMessageText", map[string]any{
		"chat_id": chatID, "receiver_user_id": userID, "ephemeral_message_id": ephemeralID,
		"text": text, "parse_mode": "HTML", "reply_markup": kb,
		"link_preview_options": LinkPreviewOptions{IsDisabled: true},
	}, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

// EditEphemeralPhoto replaces the photo of an ephemeral message.
func (c *Client) EditEphemeralPhoto(ctx context.Context, chatID, userID int64, ephemeralID int, png []byte, caption string, kb *InlineKeyboard) error {
	params := map[string]any{
		"chat_id": chatID, "receiver_user_id": userID, "ephemeral_message_id": ephemeralID,
		"media":        inputMediaPhoto{Type: "photo", Media: "attach://p", Caption: caption, ParseMode: "HTML"},
		"reply_markup": kb,
	}
	return c.CallUpload(ctx, "editEphemeralMessageMedia", params, []Upload{{Field: "p", Name: "challenge.png", Data: png}}, nil)
}

// EditEphemeralReplyMarkup replaces the keyboard of an ephemeral message.
func (c *Client) EditEphemeralReplyMarkup(ctx context.Context, chatID, userID int64, ephemeralID int, kb *InlineKeyboard) error {
	err := c.Call(ctx, "editEphemeralMessageReplyMarkup", map[string]any{
		"chat_id": chatID, "receiver_user_id": userID, "ephemeral_message_id": ephemeralID, "reply_markup": kb,
	}, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

// DeleteEphemeral deletes an ephemeral message.
func (c *Client) DeleteEphemeral(ctx context.Context, chatID, userID int64, ephemeralID int) error {
	return c.Call(ctx, "deleteEphemeralMessage", map[string]any{
		"chat_id": chatID, "receiver_user_id": userID, "ephemeral_message_id": ephemeralID,
	}, nil)
}

// DeleteMessage deletes one message.
func (c *Client) DeleteMessage(ctx context.Context, chatID int64, messageID int) error {
	return c.Call(ctx, "deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID}, nil)
}

// DeleteMessages deletes up to 100 messages at once.
func (c *Client) DeleteMessages(ctx context.Context, chatID int64, ids []int) error {
	for len(ids) > 0 {
		n := min(len(ids), 100)
		if err := c.Call(ctx, "deleteMessages", map[string]any{"chat_id": chatID, "message_ids": ids[:n]}, nil); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

// AnswerCallbackParams are answerCallbackQuery parameters.
type AnswerCallbackParams struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
	URL             string `json:"url,omitempty"`
	CacheTime       int    `json:"cache_time,omitempty"`
}

// AnswerCallback answers a callback query.
func (c *Client) AnswerCallback(ctx context.Context, p AnswerCallbackParams) error {
	return c.Call(ctx, "answerCallbackQuery", p, nil)
}

// RestrictChatMember sets member permissions until until (zero = forever).
func (c *Client) RestrictChatMember(ctx context.Context, chatID, userID int64, perms ChatPermissions, until time.Time) error {
	p := map[string]any{
		"chat_id": chatID, "user_id": userID, "permissions": perms,
		"use_independent_chat_permissions": true,
	}
	if !until.IsZero() {
		p["until_date"] = until.Unix()
	}
	return c.Call(ctx, "restrictChatMember", p, nil)
}

// BanChatMember bans a user until until (zero = forever).
func (c *Client) BanChatMember(ctx context.Context, chatID, userID int64, until time.Time, revoke bool) error {
	p := map[string]any{"chat_id": chatID, "user_id": userID, "revoke_messages": revoke}
	if !until.IsZero() {
		p["until_date"] = until.Unix()
	}
	return c.Call(ctx, "banChatMember", p, nil)
}

// UnbanChatMember lifts a ban. onlyIfBanned avoids kicking present members.
func (c *Client) UnbanChatMember(ctx context.Context, chatID, userID int64, onlyIfBanned bool) error {
	return c.Call(ctx, "unbanChatMember", map[string]any{"chat_id": chatID, "user_id": userID, "only_if_banned": onlyIfBanned}, nil)
}

// BanChatSenderChat bans a channel identity from posting in the chat.
func (c *Client) BanChatSenderChat(ctx context.Context, chatID, senderChatID int64) error {
	return c.Call(ctx, "banChatSenderChat", map[string]any{"chat_id": chatID, "sender_chat_id": senderChatID}, nil)
}

// ApproveJoinRequest approves a join request.
func (c *Client) ApproveJoinRequest(ctx context.Context, chatID, userID int64) error {
	return c.Call(ctx, "approveChatJoinRequest", map[string]any{"chat_id": chatID, "user_id": userID}, nil)
}

// DeclineJoinRequest declines a join request.
func (c *Client) DeclineJoinRequest(ctx context.Context, chatID, userID int64) error {
	return c.Call(ctx, "declineChatJoinRequest", map[string]any{"chat_id": chatID, "user_id": userID}, nil)
}

// Join request query results.
const (
	JoinApprove = "approve"
	JoinDecline = "decline"
	JoinQueue   = "queue"
)

// AnswerJoinRequestQuery resolves a Guard join request query.
func (c *Client) AnswerJoinRequestQuery(ctx context.Context, queryID, result string) error {
	return c.Call(ctx, "answerChatJoinRequestQuery", map[string]any{"chat_join_request_query_id": queryID, "result": result}, nil)
}

// SendJoinRequestWebApp shows a Mini App to a Guard join request.
func (c *Client) SendJoinRequestWebApp(ctx context.Context, queryID, url string) error {
	return c.Call(ctx, "sendChatJoinRequestWebApp", map[string]any{"chat_join_request_query_id": queryID, "web_app_url": url}, nil)
}

// GetMe returns the bot user.
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	return &u, c.Call(ctx, "getMe", nil, &u)
}

// GetChat returns full chat info.
func (c *Client) GetChat(ctx context.Context, chatID int64) (*ChatFullInfo, error) {
	var ch ChatFullInfo
	return &ch, c.Call(ctx, "getChat", map[string]any{"chat_id": chatID}, &ch)
}

// GetChatMember returns a member's status.
func (c *Client) GetChatMember(ctx context.Context, chatID, userID int64) (*ChatMember, error) {
	var m ChatMember
	return &m, c.Call(ctx, "getChatMember", map[string]any{"chat_id": chatID, "user_id": userID}, &m)
}

// GetChatAdministrators lists human administrators.
func (c *Client) GetChatAdministrators(ctx context.Context, chatID int64) ([]ChatMember, error) {
	var ms []ChatMember
	return ms, c.Call(ctx, "getChatAdministrators", map[string]any{"chat_id": chatID}, &ms)
}

// GetChatMemberCount returns the member count.
func (c *Client) GetChatMemberCount(ctx context.Context, chatID int64) (int, error) {
	var n int
	return n, c.Call(ctx, "getChatMemberCount", map[string]any{"chat_id": chatID}, &n)
}

// PinChatMessage pins a message silently.
func (c *Client) PinChatMessage(ctx context.Context, chatID int64, messageID int) error {
	return c.Call(ctx, "pinChatMessage", map[string]any{"chat_id": chatID, "message_id": messageID, "disable_notification": true}, nil)
}

// UnpinChatMessage unpins a message; messageID 0 unpins the most recent one.
func (c *Client) UnpinChatMessage(ctx context.Context, chatID int64, messageID int) error {
	p := map[string]any{"chat_id": chatID}
	if messageID != 0 {
		p["message_id"] = messageID
	}
	return c.Call(ctx, "unpinChatMessage", p, nil)
}

// UnpinAllChatMessages unpins everything.
func (c *Client) UnpinAllChatMessages(ctx context.Context, chatID int64) error {
	return c.Call(ctx, "unpinAllChatMessages", map[string]any{"chat_id": chatID}, nil)
}

// LeaveChat makes the bot leave a chat.
func (c *Client) LeaveChat(ctx context.Context, chatID int64) error {
	return c.Call(ctx, "leaveChat", map[string]any{"chat_id": chatID}, nil)
}

// SetMyCommands registers commands for a scope.
func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand, scope *BotCommandScope, lang string) error {
	p := map[string]any{"commands": cmds}
	if scope != nil {
		p["scope"] = scope
	}
	if lang != "" {
		p["language_code"] = lang
	}
	return c.Call(ctx, "setMyCommands", p, nil)
}

// GetWebhookInfo returns webhook status.
func (c *Client) GetWebhookInfo(ctx context.Context) (*WebhookInfo, error) {
	var w WebhookInfo
	return &w, c.Call(ctx, "getWebhookInfo", nil, &w)
}

// DeleteWebhook removes any webhook so long polling works.
func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.Call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
}

// AllowedUpdates is the update set TAdmin subscribes to.
var AllowedUpdates = []string{"message", "callback_query", "chat_member", "my_chat_member", "chat_join_request"}

// GetUpdates long-polls for updates.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	var ups []Update
	err := c.do(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": int(timeout.Seconds()), "allowed_updates": AllowedUpdates,
	}, nil, &ups)
	return ups, err
}

// UserLink returns a tg:// mention link for a user id.
func UserLink(id int64) string { return "tg://user?id=" + strconv.FormatInt(id, 10) }

// EditEphemeralCaption edits the caption and keyboard of an ephemeral media message.
func (c *Client) EditEphemeralCaption(ctx context.Context, chatID, userID int64, ephemeralID int, caption string, kb *InlineKeyboard) error {
	err := c.Call(ctx, "editEphemeralMessageCaption", map[string]any{
		"chat_id": chatID, "receiver_user_id": userID, "ephemeral_message_id": ephemeralID,
		"caption": caption, "parse_mode": "HTML", "reply_markup": kb,
	}, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}
