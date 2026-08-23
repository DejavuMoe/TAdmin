package moderation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"

	"tadmin/internal/store"
)

const (
	coordinatorInterval = 2 * time.Second
	actionRetryAge      = 15 * time.Second
)

type Options struct {
	OwnerUserID      int64
	BotUserID        int64
	ManagementChatID int64
	BotToken         string
}

type Coordinator struct {
	database  *store.Store
	telegram  Telegram
	logger    *slog.Logger
	options   Options
	digestKey [sha256.Size]byte
	now       func() time.Time
	wake      chan struct{}
}

func New(database *store.Store, telegram Telegram, logger *slog.Logger, options Options) *Coordinator {
	deriver := hmac.New(sha256.New, []byte(options.BotToken))
	_, _ = deriver.Write([]byte("TAdmin moderation content digest v1"))
	var digestKey [sha256.Size]byte
	copy(digestKey[:], deriver.Sum(nil))
	return &Coordinator{
		database: database, telegram: telegram, logger: logger, options: options,
		digestKey: digestKey, now: time.Now, wake: make(chan struct{}, 1),
	}
}

func (c *Coordinator) Wake() {
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Coordinator) Run(ctx context.Context) {
	if c == nil || c.database == nil || c.telegram == nil {
		return
	}
	ticker := time.NewTicker(coordinatorInterval)
	defer ticker.Stop()
	c.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.reconcile(ctx)
		case <-c.wake:
			c.reconcile(ctx)
		}
	}
}

func (c *Coordinator) HandleMessage(ctx context.Context, updateID int64, message *models.Message, edited bool) error {
	if c == nil || message == nil || message.Chat.ID == 0 || message.ID <= 0 ||
		message.Chat.Type != models.ChatTypeSupergroup || isServiceMessage(message) || isBotCommand(message) {
		return nil
	}
	targetKind, targetID, ok := messageTarget(message)
	if !ok || (targetKind == store.ModerationTargetUser && (targetID == c.options.OwnerUserID || targetID == c.options.BotUserID)) {
		return nil
	}
	text, entities := message.Text, message.Entities
	if text == "" {
		text, entities = message.Caption, message.CaptionEntities
	}
	analysis := analyzeText(text, entities)
	media, mediaKey := mediaIdentity(message)
	if analysis.Normalized == "" && !media && !messageIsForward(message) {
		return nil
	}
	canonical := analysis.Normalized + "\x00" + mediaKey
	if messageIsForward(message) {
		canonical += "\x00" + forwardIdentity(message.ForwardOrigin)
	}
	digest := hmac.New(sha256.New, c.digestKey[:])
	_, _ = digest.Write([]byte(canonical))
	contentHash := hex.EncodeToString(digest.Sum(nil))

	now := c.currentTime()
	messageAt := telegramTime(message.Date, now)
	versionUnix := message.Date
	if edited && message.EditDate > 0 {
		versionUnix = message.EditDate
	}
	versionAt := telegramTime(versionUnix, now)
	_, err := c.database.AssessModerationMessage(ctx, store.ModerationMessage{
		UpdateID: updateID, ChatID: message.Chat.ID, MessageID: message.ID,
		TargetKind: targetKind, TargetID: targetID, ContentHash: contentHash,
		Domains: analysis.Domains, HasExternalLink: analysis.HasExternalLink,
		HasTelegramLink: analysis.HasTelegramLink, HasObfuscation: analysis.HasObfuscation,
		HasMedia: media, IsForward: messageIsForward(message), MentionCount: analysis.MentionCount,
		MessageAt: messageAt, VersionAt: versionAt, Now: now,
	})
	if err != nil {
		return err
	}
	c.Wake()
	return nil
}

func (c *Coordinator) IsAdministrator(ctx context.Context, chatID, userID int64) (bool, error) {
	if userID == c.options.OwnerUserID {
		return true, nil
	}
	checkContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return c.telegram.IsAdministrator(checkContext, chatID, userID)
}

func (c *Coordinator) UpdateList(ctx context.Context, operationKey string, chatID, userID int64, kind string, add bool, actorUserID int64) (bool, error) {
	changed, err := c.database.UpdateListEntry(ctx, operationKey, chatID, userID, kind, add, actorUserID, c.currentTime())
	if err == nil && changed {
		c.Wake()
	}
	return changed, err
}

func (c *Coordinator) SetRaidMode(ctx context.Context, chatID int64, mode string, actorUserID int64) (store.RaidState, bool, error) {
	state, changed, err := c.database.SetRaidMode(ctx, chatID, mode, actorUserID, c.currentTime())
	if err == nil && changed {
		c.Wake()
	}
	return state, changed, err
}

func (c *Coordinator) Status(ctx context.Context, chatID int64) (store.ModerationStatus, error) {
	now := c.currentTime()
	if err := c.database.RefreshRaidStates(ctx, now); err != nil {
		return store.ModerationStatus{}, err
	}
	return c.database.GetModerationStatus(ctx, chatID, now)
}

func (c *Coordinator) ScheduleManual(ctx context.Context, commandMessageID int, chatID, targetUserID, actorUserID int64, actionKind string, duration time.Duration) (bool, error) {
	var untilAt *time.Time
	if actionKind == store.ModerationMute {
		value := c.currentTime().Add(duration)
		untilAt = &value
	}
	key := fmt.Sprintf("manual:%d:%d:%s:%d", chatID, commandMessageID, actionKind, targetUserID)
	created, err := c.database.ScheduleManualModeration(ctx, store.ManualModerationRequest{
		IdempotencyKey: key, ChatID: chatID, CommandMessageID: commandMessageID,
		TargetUserID: targetUserID, ActorUserID: actorUserID,
		ActionKind: actionKind, UntilAt: untilAt, Reason: "manual_" + actionKind,
	}, c.currentTime())
	if err == nil && created {
		c.Wake()
	}
	return created, err
}

func (c *Coordinator) reconcile(ctx context.Context) {
	now := c.currentTime()
	if err := c.database.RefreshRaidStates(ctx, now); err != nil {
		c.logError("refresh Raid state", store.ModerationAction{}, err)
		return
	}
	if err := c.database.SettleStaleUnreplayableActions(ctx, now.Add(-actionRetryAge), now); err != nil {
		c.logError("settle stale moderation actions", store.ModerationAction{}, err)
		return
	}
	actions, err := c.database.ClaimModerationActions(ctx, now.Add(-actionRetryAge), now, 50)
	if err != nil {
		c.logError("claim moderation actions", store.ModerationAction{}, err)
		return
	}
	for _, action := range actions {
		select {
		case <-ctx.Done():
			return
		default:
		}
		result, err := c.executeAction(ctx, action)
		if err != nil {
			_ = c.database.FailModerationAction(ctx, action.ID, c.currentTime())
			c.logError("execute moderation action", action, err)
			continue
		}
		if err := c.database.CompleteModerationAction(ctx, action.ID, result, c.currentTime()); err != nil {
			c.logError("complete moderation action", action, err)
			continue
		}
		if action.ActionKind != store.ModerationNotify && action.ActionKind != store.ModerationLog &&
			action.ActionKind != store.ModerationRaidNotice {
			c.Wake()
		}
	}
}

func (c *Coordinator) executeAction(ctx context.Context, action store.ModerationAction) (string, error) {
	chat, err := c.database.GetManagedChat(ctx, action.ChatID)
	if err != nil {
		return "", err
	}
	if action.ActionKind == store.ModerationLog || action.ActionKind == store.ModerationRaidNotice {
		if c.options.ManagementChatID == 0 || chat.ManagementTopicID == 0 {
			return "", ErrManagementTopicUnavailable
		}
		text := moderationLog(chat.Language, action)
		if action.ActionKind == store.ModerationRaidNotice {
			text = raidNotice(chat.Language, action)
		}
		requestContext, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if err := c.telegram.SendManagementLog(requestContext, c.options.ManagementChatID, chat.ManagementTopicID, text); err != nil {
			return "", err
		}
		return "completed", nil
	}
	if action.ActionKind == store.ModerationNotify {
		requestContext, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return "completed", c.telegram.SendModerationNotice(requestContext, action.ChatID,
			moderationNotice(chat.Language, sourceAction(action), action.TargetKind, action.TargetID, action.UntilAt))
	}
	if action.TargetKind == store.ModerationTargetUser {
		if action.TargetID == c.options.OwnerUserID {
			return "skipped_owner", nil
		}
		checkContext, cancel := context.WithTimeout(ctx, 8*time.Second)
		administrator, err := c.telegram.IsAdministrator(checkContext, action.ChatID, action.TargetID)
		cancel()
		if err != nil {
			return "", err
		}
		if administrator {
			return "skipped_admin", nil
		}
	}
	requestContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	switch action.ActionKind {
	case store.ModerationDeleteWarn:
		if err := c.telegram.DeleteModerationMessage(requestContext, action.ChatID, action.MessageID); err != nil {
			return "", err
		}
		return "completed", nil
	case store.ModerationDeleteMute:
		if action.TargetKind != store.ModerationTargetUser || action.UntilAt == nil {
			return "skipped_sender_chat", nil
		}
		if err := c.telegram.DeleteModerationMessage(requestContext, action.ChatID, action.MessageID); err != nil {
			return "", err
		}
		if err := c.telegram.RestrictMemberUntil(requestContext, action.ChatID, action.TargetID, *action.UntilAt); err != nil {
			return "", err
		}
		return "completed", nil
	case store.ModerationDeleteBan:
		if action.TargetKind != store.ModerationTargetUser {
			return "skipped_sender_chat", nil
		}
		if err := c.telegram.DeleteModerationMessage(requestContext, action.ChatID, action.MessageID); err != nil {
			return "", err
		}
		if err := c.telegram.BanMember(requestContext, action.ChatID, action.TargetID); err != nil {
			return "", err
		}
		return "completed", nil
	case store.ModerationWarn:
		return "completed", c.telegram.SendModerationNotice(requestContext, action.ChatID, moderationNotice(chat.Language, action.ActionKind, action.TargetKind, action.TargetID, nil))
	case store.ModerationMute:
		if action.UntilAt == nil {
			return "", store.ErrModerationState
		}
		if err := c.telegram.RestrictMemberUntil(requestContext, action.ChatID, action.TargetID, *action.UntilAt); err != nil {
			return "", err
		}
		return "completed", nil
	case store.ModerationKick:
		if err := c.telegram.KickMember(requestContext, action.ChatID, action.TargetID); err != nil {
			return "", err
		}
		return "completed", nil
	case store.ModerationBan:
		if err := c.telegram.BanMember(requestContext, action.ChatID, action.TargetID); err != nil {
			return "", err
		}
		return "completed", nil
	default:
		return "", store.ErrModerationState
	}
}

var ErrManagementTopicUnavailable = errors.New("management topic is unavailable")

func messageTarget(message *models.Message) (string, int64, bool) {
	if message.SenderChat != nil {
		if message.SenderChat.ID == message.Chat.ID {
			return "", 0, false
		}
		return store.ModerationTargetSenderChat, message.SenderChat.ID, message.SenderChat.ID != 0
	}
	if message.From == nil {
		return "", 0, false
	}
	return store.ModerationTargetUser, message.From.ID, message.From.ID > 0
}

func isServiceMessage(message *models.Message) bool {
	if message.Text != "" || message.Caption != "" {
		return false
	}
	media, _ := mediaIdentity(message)
	return !media && !messageIsForward(message)
}

func isBotCommand(message *models.Message) bool {
	if message.Text == "" {
		return false
	}
	for _, entity := range message.Entities {
		if entity.Offset == 0 && entity.Type == models.MessageEntityTypeBotCommand {
			return true
		}
	}
	return false
}

func messageIsForward(message *models.Message) bool {
	return message.ForwardOrigin != nil || message.IsAutomaticForward
}

func forwardIdentity(origin *models.MessageOrigin) string {
	if origin == nil {
		return "automatic_forward"
	}
	switch origin.Type {
	case models.MessageOriginTypeUser:
		if value := origin.MessageOriginUser; value != nil {
			return fmt.Sprintf("forward:user:%d:%d", value.SenderUser.ID, value.Date)
		}
	case models.MessageOriginTypeHiddenUser:
		if value := origin.MessageOriginHiddenUser; value != nil {
			return fmt.Sprintf("forward:hidden:%s:%d", value.SenderUserName, value.Date)
		}
	case models.MessageOriginTypeChat:
		if value := origin.MessageOriginChat; value != nil {
			return fmt.Sprintf("forward:chat:%d:%d", value.SenderChat.ID, value.Date)
		}
	case models.MessageOriginTypeChannel:
		if value := origin.MessageOriginChannel; value != nil {
			return fmt.Sprintf("forward:channel:%d:%d", value.Chat.ID, value.MessageID)
		}
	}
	return "forward"
}

func mediaIdentity(message *models.Message) (bool, string) {
	switch {
	case message.Animation != nil:
		return true, "animation:" + message.Animation.FileUniqueID
	case message.Audio != nil:
		return true, "audio:" + message.Audio.FileUniqueID
	case message.Document != nil:
		return true, "document:" + message.Document.FileUniqueID
	case len(message.Photo) > 0:
		return true, "photo:" + message.Photo[len(message.Photo)-1].FileUniqueID
	case message.Sticker != nil:
		return true, "sticker:" + message.Sticker.FileUniqueID
	case message.Video != nil:
		return true, "video:" + message.Video.FileUniqueID
	case message.VideoNote != nil:
		return true, "video_note:" + message.VideoNote.FileUniqueID
	case message.Voice != nil:
		return true, "voice:" + message.Voice.FileUniqueID
	case message.PaidMedia != nil:
		return true, "paid_media"
	case message.Story != nil:
		return true, "story:" + strconv.Itoa(message.Story.ID)
	default:
		return false, ""
	}
}

func telegramTime(value int, fallback time.Time) time.Time {
	if value <= 0 {
		return fallback
	}
	parsed := time.Unix(int64(value), 0).UTC()
	if parsed.After(fallback.Add(time.Minute)) {
		return fallback
	}
	return parsed
}

func (c *Coordinator) currentTime() time.Time {
	return c.now().UTC().Truncate(time.Second)
}

func (c *Coordinator) logError(operation string, action store.ModerationAction, err error) {
	if c.logger == nil || err == nil {
		return
	}
	attributes := []any{"error", err}
	if action.ID != 0 {
		attributes = append(attributes, "moderation_action_id", action.ID, "chat_id", action.ChatID, "action", action.ActionKind)
	}
	c.logger.Error(operation, attributes...)
}

func ParseUserID(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil && parsed > 0
}
