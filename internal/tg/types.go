// Package tg is a small, dependency-free Telegram Bot API client tailored to
// TAdmin: typed models for the subset of the API the bot uses, error
// classification, per-chat send throttling and 429 back-off.
package tg

import "encoding/json"

// Update is one incoming update from getUpdates.
type Update struct {
	UpdateID        int64              `json:"update_id"`
	Message         *Message           `json:"message,omitempty"`
	EditedMessage   *Message           `json:"edited_message,omitempty"`
	ChannelPost     *Message           `json:"channel_post,omitempty"`
	CallbackQuery   *CallbackQuery     `json:"callback_query,omitempty"`
	MyChatMember    *ChatMemberUpdated `json:"my_chat_member,omitempty"`
	ChatMember      *ChatMemberUpdated `json:"chat_member,omitempty"`
	ChatJoinRequest *ChatJoinRequest   `json:"chat_join_request,omitempty"`
}

// User is a Telegram user or bot.
type User struct {
	ID                         int64  `json:"id"`
	IsBot                      bool   `json:"is_bot"`
	FirstName                  string `json:"first_name"`
	LastName                   string `json:"last_name,omitempty"`
	Username                   string `json:"username,omitempty"`
	LanguageCode               string `json:"language_code,omitempty"`
	IsPremium                  bool   `json:"is_premium,omitempty"`
	CanJoinGroups              bool   `json:"can_join_groups,omitempty"`
	SupportsJoinRequestQueries bool   `json:"supports_join_request_queries,omitempty"`
}

// FullName returns "First Last" trimmed.
func (u *User) FullName() string {
	if u == nil {
		return ""
	}
	if u.LastName == "" {
		return u.FirstName
	}
	return u.FirstName + " " + u.LastName
}

// Chat types.
const (
	ChatPrivate    = "private"
	ChatGroup      = "group"
	ChatSupergroup = "supergroup"
	ChatChannel    = "channel"
)

// Chat is the short chat object embedded in messages.
type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title,omitempty"`
	Username string `json:"username,omitempty"`
	IsForum  bool   `json:"is_forum,omitempty"`
}

// ChatFullInfo is returned by getChat.
type ChatFullInfo struct {
	ID                 int64            `json:"id"`
	Type               string           `json:"type"`
	Title              string           `json:"title,omitempty"`
	Username           string           `json:"username,omitempty"`
	FirstName          string           `json:"first_name,omitempty"`
	LastName           string           `json:"last_name,omitempty"`
	IsForum            bool             `json:"is_forum,omitempty"`
	Bio                string           `json:"bio,omitempty"`
	Description        string           `json:"description,omitempty"`
	InviteLink         string           `json:"invite_link,omitempty"`
	PinnedMessage      *Message         `json:"pinned_message,omitempty"`
	Permissions        *ChatPermissions `json:"permissions,omitempty"`
	JoinToSendMessages bool             `json:"join_to_send_messages,omitempty"`
	JoinByRequest      bool             `json:"join_by_request,omitempty"`
	LinkedChatID       int64            `json:"linked_chat_id,omitempty"`
	GuardBot           *User            `json:"guard_bot,omitempty"`
	Photo              *json.RawMessage `json:"photo,omitempty"`
}

// Message is a Telegram message. Only fields TAdmin reads are modelled.
type Message struct {
	MessageID          int              `json:"message_id"`
	EphemeralMessageID int              `json:"ephemeral_message_id,omitempty"`
	ReceiverUser       *User            `json:"receiver_user,omitempty"`
	From               *User            `json:"from,omitempty"`
	SenderChat         *Chat            `json:"sender_chat,omitempty"`
	Date               int64            `json:"date"`
	Chat               Chat             `json:"chat"`
	IsAutomaticForward bool             `json:"is_automatic_forward,omitempty"`
	ReplyToMessage     *Message         `json:"reply_to_message,omitempty"`
	Text               string           `json:"text,omitempty"`
	Caption            string           `json:"caption,omitempty"`
	Entities           []MessageEntity  `json:"entities,omitempty"`
	CaptionEntities    []MessageEntity  `json:"caption_entities,omitempty"`
	Photo              []PhotoSize      `json:"photo,omitempty"`
	NewChatMembers     []User           `json:"new_chat_members,omitempty"`
	LeftChatMember     *User            `json:"left_chat_member,omitempty"`
	NewChatTitle       string           `json:"new_chat_title,omitempty"`
	NewChatPhoto       *json.RawMessage `json:"new_chat_photo,omitempty"`
	DeleteChatPhoto    bool             `json:"delete_chat_photo,omitempty"`
	PinnedMessage      *Message         `json:"pinned_message,omitempty"`
	MigrateToChatID    int64            `json:"migrate_to_chat_id,omitempty"`
	MigrateFromChatID  int64            `json:"migrate_from_chat_id,omitempty"`
	VideoChatStarted   *json.RawMessage `json:"video_chat_started,omitempty"`
	VideoChatEnded     *json.RawMessage `json:"video_chat_ended,omitempty"`
	VideoChatScheduled *json.RawMessage `json:"video_chat_scheduled,omitempty"`
	BoostAdded         *json.RawMessage `json:"boost_added,omitempty"`
	AutoDeleteChanged  *json.RawMessage `json:"message_auto_delete_timer_changed,omitempty"`
	ForumTopicCreated  *json.RawMessage `json:"forum_topic_created,omitempty"`
	ReplyMarkup        *InlineKeyboard  `json:"reply_markup,omitempty"`
}

// IsCommand reports whether the message starts with a bot command entity.
func (m *Message) IsCommand() bool {
	return m != nil && len(m.Entities) > 0 && m.Entities[0].Type == "bot_command" && m.Entities[0].Offset == 0
}

// MessageEntity is a formatting/entity span.
type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	URL    string `json:"url,omitempty"`
	User   *User  `json:"user,omitempty"`
}

// PhotoSize is one size of a photo.
type PhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int    `json:"file_size,omitempty"`
}

// CallbackQuery is an inline button press.
type CallbackQuery struct {
	ID           string   `json:"id"`
	From         User     `json:"from"`
	Message      *Message `json:"message,omitempty"`
	ChatInstance string   `json:"chat_instance"`
	Data         string   `json:"data,omitempty"`
}

// Chat member statuses.
const (
	StatusCreator       = "creator"
	StatusAdministrator = "administrator"
	StatusMember        = "member"
	StatusRestricted    = "restricted"
	StatusLeft          = "left"
	StatusKicked        = "kicked"
)

// ChatMember flattens all ChatMember variants into one struct.
type ChatMember struct {
	Status      string `json:"status"`
	User        User   `json:"user"`
	IsAnonymous bool   `json:"is_anonymous,omitempty"`
	CustomTitle string `json:"custom_title,omitempty"`
	// Restricted only.
	IsMember  bool  `json:"is_member,omitempty"`
	UntilDate int64 `json:"until_date,omitempty"`
	ChatPermissions
	// Administrator only.
	CanBeEdited         bool `json:"can_be_edited,omitempty"`
	CanManageChat       bool `json:"can_manage_chat,omitempty"`
	CanDeleteMessages   bool `json:"can_delete_messages,omitempty"`
	CanRestrictMembers  bool `json:"can_restrict_members,omitempty"`
	CanPromoteMembers   bool `json:"can_promote_members,omitempty"`
	CanSendWelcome      bool `json:"can_send_welcome_messages,omitempty"`
	CanManageVideoChats bool `json:"can_manage_video_chats,omitempty"`
}

// IsAdmin reports creator/administrator status.
func (m *ChatMember) IsAdmin() bool {
	return m != nil && (m.Status == StatusCreator || m.Status == StatusAdministrator)
}

// IsPresent reports whether the member is currently in the chat.
func (m *ChatMember) IsPresent() bool {
	if m == nil {
		return false
	}
	switch m.Status {
	case StatusCreator, StatusAdministrator, StatusMember:
		return true
	case StatusRestricted:
		return m.IsMember
	}
	return false
}

// CanRestrict reports whether an admin can restrict members.
func (m *ChatMember) CanRestrict() bool {
	return m != nil && (m.Status == StatusCreator || (m.Status == StatusAdministrator && m.CanRestrictMembers))
}

// ChatMemberUpdated is a chat_member / my_chat_member update.
type ChatMemberUpdated struct {
	Chat           Chat            `json:"chat"`
	From           User            `json:"from"`
	Date           int64           `json:"date"`
	OldChatMember  ChatMember      `json:"old_chat_member"`
	NewChatMember  ChatMember      `json:"new_chat_member"`
	InviteLink     *ChatInviteLink `json:"invite_link,omitempty"`
	ViaJoinRequest bool            `json:"via_join_request,omitempty"`
	ViaFolderLink  bool            `json:"via_chat_folder_invite_link,omitempty"`
}

// ChatInviteLink describes an invite link.
type ChatInviteLink struct {
	InviteLink         string `json:"invite_link"`
	Creator            User   `json:"creator"`
	CreatesJoinRequest bool   `json:"creates_join_request"`
	IsPrimary          bool   `json:"is_primary"`
	IsRevoked          bool   `json:"is_revoked"`
	Name               string `json:"name,omitempty"`
}

// ChatJoinRequest is a join request (optionally a Guard query).
type ChatJoinRequest struct {
	Chat       Chat            `json:"chat"`
	From       User            `json:"from"`
	UserChatID int64           `json:"user_chat_id"`
	Date       int64           `json:"date"`
	Bio        string          `json:"bio,omitempty"`
	InviteLink *ChatInviteLink `json:"invite_link,omitempty"`
	QueryID    string          `json:"query_id,omitempty"`
}

// ChatPermissions are the member permissions used by restrictChatMember.
// Pointers distinguish "unset" from false when decoding member state.
type ChatPermissions struct {
	CanSendMessages       *bool `json:"can_send_messages,omitempty"`
	CanSendAudios         *bool `json:"can_send_audios,omitempty"`
	CanSendDocuments      *bool `json:"can_send_documents,omitempty"`
	CanSendPhotos         *bool `json:"can_send_photos,omitempty"`
	CanSendVideos         *bool `json:"can_send_videos,omitempty"`
	CanSendVideoNotes     *bool `json:"can_send_video_notes,omitempty"`
	CanSendVoiceNotes     *bool `json:"can_send_voice_notes,omitempty"`
	CanSendPolls          *bool `json:"can_send_polls,omitempty"`
	CanSendOtherMessages  *bool `json:"can_send_other_messages,omitempty"`
	CanAddWebPagePreviews *bool `json:"can_add_web_page_previews,omitempty"`
	CanReactToMessages    *bool `json:"can_react_to_messages,omitempty"`
	CanChangeInfo         *bool `json:"can_change_info,omitempty"`
	CanInviteUsers        *bool `json:"can_invite_users,omitempty"`
	CanPinMessages        *bool `json:"can_pin_messages,omitempty"`
	CanManageTopics       *bool `json:"can_manage_topics,omitempty"`
}

// AllPermissions returns permissions with every flag set to v.
func AllPermissions(v bool) ChatPermissions {
	b := func() *bool { x := v; return &x }
	return ChatPermissions{
		CanSendMessages: b(), CanSendAudios: b(), CanSendDocuments: b(), CanSendPhotos: b(),
		CanSendVideos: b(), CanSendVideoNotes: b(), CanSendVoiceNotes: b(), CanSendPolls: b(),
		CanSendOtherMessages: b(), CanAddWebPagePreviews: b(), CanReactToMessages: b(),
		CanChangeInfo: b(), CanInviteUsers: b(), CanPinMessages: b(), CanManageTopics: b(),
	}
}

// Muted returns permissions that forbid sending anything but keep reactions
// and inviting off as well: the member can only read and press buttons.
func Muted() ChatPermissions { return AllPermissions(false) }

// InlineKeyboard is an inline keyboard markup.
type InlineKeyboard struct {
	InlineKeyboard [][]Button `json:"inline_keyboard"`
}

// Button styles (Bot API 9.4).
const (
	StylePrimary = "primary"
	StyleSuccess = "success"
	StyleDanger  = "danger"
)

// Button is an inline keyboard button.
type Button struct {
	Text         string          `json:"text"`
	Style        string          `json:"style,omitempty"`
	URL          string          `json:"url,omitempty"`
	CallbackData string          `json:"callback_data,omitempty"`
	WebApp       *WebAppInfo     `json:"web_app,omitempty"`
	CopyText     *CopyTextButton `json:"copy_text,omitempty"`
	Disabled     *struct{}       `json:"disabled,omitempty"`
}

// WebAppInfo points at a Mini App URL.
type WebAppInfo struct {
	URL string `json:"url"`
}

// CopyTextButton copies text to the clipboard.
type CopyTextButton struct {
	Text string `json:"text"`
}

// LinkPreviewOptions controls link previews.
type LinkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled,omitempty"`
}

// ReplyParameters describes the message to reply to.
type ReplyParameters struct {
	MessageID                int  `json:"message_id,omitempty"`
	EphemeralMessageID       int  `json:"ephemeral_message_id,omitempty"`
	AllowSendingWithoutReply bool `json:"allow_sending_without_reply,omitempty"`
}

// EphemeralParams makes a send call ephemeral (Bot API 10.3).
type EphemeralParams struct {
	ReceiverUserID          int64  `json:"receiver_user_id"`
	CallbackQueryID         string `json:"callback_query_id,omitempty"`
	ReplaceCallbackQueryMsg bool   `json:"replace_callback_query_message,omitempty"`
}

// BotCommand is a command shown in the client menu.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	IsEphemeral bool   `json:"is_ephemeral,omitempty"`
}

// BotCommandScope limits where commands are shown.
type BotCommandScope struct {
	Type   string `json:"type"`
	ChatID int64  `json:"chat_id,omitempty"`
	UserID int64  `json:"user_id,omitempty"`
}

// WebhookInfo is returned by getWebhookInfo.
type WebhookInfo struct {
	URL                string `json:"url"`
	PendingUpdateCount int    `json:"pending_update_count"`
}

// Bool returns a pointer to v.
func Bool(v bool) *bool { return &v }
