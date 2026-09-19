package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	telegrambot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"tadmin/internal/admin"
	"tadmin/internal/config"
	"tadmin/internal/i18n"
	"tadmin/internal/join"
	"tadmin/internal/store"
	"tadmin/internal/verify"
)

type commandHandler struct {
	cfg       config.Config
	database  *store.Store
	logger    *slog.Logger
	botInfo   BotInfo
	version   string
	startedAt time.Time
	messages  i18n.Messages
	joins     *join.Coordinator
}

type botPermissions struct {
	administrator      bool
	canInviteUsers     bool
	canRestrictMembers bool
	canDeleteMessages  bool
	canManageTopics    bool
}

func newCommandHandler(
	cfg config.Config,
	database *store.Store,
	logger *slog.Logger,
	botInfo BotInfo,
	version string,
	startedAt time.Time,
	joins *join.Coordinator,
) *commandHandler {
	return &commandHandler{
		cfg:       cfg,
		database:  database,
		logger:    logger,
		botInfo:   botInfo,
		version:   version,
		startedAt: startedAt,
		messages:  i18n.For(cfg.DefaultLanguage),
		joins:     joins,
	}
}

func (h *commandHandler) handle(ctx context.Context, bot *telegrambot.Bot, update *models.Update) {
	message := update.Message
	if message == nil || message.Text == "" {
		return
	}
	if !hasLeadingBotCommand(message.Entities) {
		return
	}

	command, targetUsername, arguments, ok := parseCommand(message.Text)
	if !ok {
		return
	}
	if targetUsername != "" && !strings.EqualFold(targetUsername, h.botInfo.Username) {
		return
	}

	if command == "start" {
		if message.Chat.Type == models.ChatTypePrivate && message.From != nil {
			if len(arguments) == 1 && strings.HasPrefix(arguments[0], "verify_") && h.joins != nil {
				token := strings.TrimPrefix(arguments[0], "verify_")
				err := h.joins.HandlePrivateStart(ctx, message.Chat.ID, message.From.ID, token)
				switch {
				case errors.Is(err, join.ErrStartIdentity):
					h.send(ctx, bot, message.Chat.ID, h.messages.StartVerifyWrong)
				case err != nil:
					h.send(ctx, bot, message.Chat.ID, h.messages.StartVerifyInvalid)
				}
			} else {
				h.send(ctx, bot, message.Chat.ID, fmt.Sprintf(h.messages.Start, h.version))
			}
		}
		return
	}
	if command == "help" {
		h.send(ctx, bot, message.Chat.ID, h.messages.Usage)
		return
	}
	if command != "tadmin" {
		return
	}

	if message.SenderChat != nil || message.From == nil || message.From.ID == 1087968824 {
		h.send(ctx, bot, message.Chat.ID, h.messages.Unauthorized)
		return
	}
	owner := admin.IsOwner(h.cfg.OwnerUserID, message.From.ID)
	if len(arguments) == 1 && strings.EqualFold(arguments[0], "status") {
		if !owner {
			h.send(ctx, bot, message.Chat.ID, h.messages.Unauthorized)
			return
		}
		h.status(ctx, bot, message.Chat.ID)
		return
	}
	if !owner {
		h.send(ctx, bot, message.Chat.ID, h.messages.Unauthorized)
		return
	}
	if len(arguments) == 1 && arguments[0] == "groups" {
		chats, err := h.database.ListManagedChats(ctx)
		if err != nil {
			h.send(ctx, bot, message.Chat.ID, h.messages.SetupCheckFailed)
			return
		}
		text := h.messages.ManagedChats
		for _, chat := range chats {
			text += fmt.Sprintf("\n%s (%d) · enabled=%t · %ds", chat.Title, chat.ChatID, chat.Enabled, chat.VerificationTimeoutSeconds)
		}
		h.send(ctx, bot, message.Chat.ID, text)
		return
	}
	if len(arguments) >= 1 && (arguments[0] == "setup" || arguments[0] == "timeout") {
		target := *message
		if message.Chat.Type == models.ChatTypePrivate {
			if len(arguments) < 2 {
				h.send(ctx, bot, message.Chat.ID, h.messages.Usage)
				return
			}
			id, err := strconv.ParseInt(arguments[1], 10, 64)
			if err != nil || id >= 0 {
				h.send(ctx, bot, message.Chat.ID, h.messages.Usage)
				return
			}
			target.Chat = models.Chat{ID: id, Type: models.ChatTypeSupergroup}
			arguments = append(arguments[:1:1], arguments[2:]...)
		}
		if arguments[0] == "setup" && len(arguments) == 1 {
			h.setupFor(ctx, bot, &target, message.Chat.ID)
			return
		}
		if arguments[0] == "timeout" && len(arguments) == 2 {
			seconds, err := strconv.Atoi(arguments[1])
			if err == nil && seconds >= 30 && seconds <= 900 {
				err = h.database.SetVerificationTimeout(ctx, target.Chat.ID, seconds, message.From.ID, time.Now())
				if err == nil {
					h.send(ctx, bot, message.Chat.ID, fmt.Sprintf("%d: %ds", target.Chat.ID, seconds))
					return
				}
			}
		}
	}
	h.send(ctx, bot, message.Chat.ID, h.messages.Usage)
}

func (h *commandHandler) status(ctx context.Context, bot *telegrambot.Bot, chatID int64) {
	checkContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	databaseState := h.messages.Healthy
	managedChats, err := h.database.CountManagedChats(checkContext)
	if err != nil {
		databaseState = h.messages.Unhealthy
		h.logger.Error("status database check failed", "error", err)
	}

	text := fmt.Sprintf(
		"%s\n%s: %s\n%s: %s\n%s: %s\n%s: %d\n%s: %s",
		h.messages.StatusTitle,
		h.messages.Version, h.version,
		h.messages.Uptime, time.Since(h.startedAt).Truncate(time.Second),
		h.messages.Database, databaseState,
		h.messages.ManagedChats, managedChats,
		h.messages.JoinRequestQueries, yesNo(h.messages, h.botInfo.SupportsJoinRequestQueries),
	)
	active, failed, cards, countErr := h.database.VerificationCounts(checkContext)
	if countErr == nil {
		if h.cfg.DefaultLanguage == "en" {
			text += fmt.Sprintf("\nActive admissions: %d; failed actions: %d; pending cards: %d", active, failed, cards)
		} else {
			text += fmt.Sprintf("\n进行中入群：%d；失败待重试：%d；待同步卡片：%d", active, failed, cards)
		}
	}
	h.send(ctx, bot, chatID, text)
}

func (h *commandHandler) setup(ctx context.Context, bot *telegrambot.Bot, message *models.Message) {
	h.setupFor(ctx, bot, message, message.Chat.ID)
}

func (h *commandHandler) setupFor(ctx context.Context, bot *telegrambot.Bot, message *models.Message, replyChatID int64) {
	if message.Chat.Type != models.ChatTypeSupergroup {
		h.send(ctx, bot, replyChatID, h.messages.SetupSupergroupOnly)
		return
	}

	if _, err := verify.VerificationURL(h.cfg.PublicBaseURL, strings.Repeat("a", 32)); err != nil {
		h.send(ctx, bot, replyChatID, h.messages.SetupPublicURL)
		return
	}
	destination := h.cfg.ManagementChatID
	if destination == 0 {
		destination = h.cfg.OwnerUserID
	}

	requestContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	source, err := bot.GetChat(requestContext, &telegrambot.GetChatParams{ChatID: message.Chat.ID})
	if err != nil || source == nil || source.Type != models.ChatTypeSupergroup || source.IsForum {
		h.send(ctx, bot, replyChatID, h.messages.SetupSupergroupOnly)
		return
	}
	if source.LinkedChatID != 0 && !source.JoinToSendMessages {
		h.send(ctx, bot, replyChatID, "请在 Telegram 讨论群设置中启用「加入后才能发言」，再接入。 / Enable join-to-send in the linked discussion group before setup.")
		return
	}
	if source.UnrestrictBoostCount > 0 {
		h.send(ctx, bot, replyChatID, "请关闭助推用户绕过发言限制，再接入。 / Disable boost-based permission bypass before setup.")
		return
	}
	message.Chat.Title = source.Title
	member, err := bot.GetChatMember(requestContext, &telegrambot.GetChatMemberParams{
		ChatID: message.Chat.ID,
		UserID: h.botInfo.ID,
	})
	if err != nil {
		h.logger.Error("telegram permission check failed",
			"chat_id", message.Chat.ID,
			"error", sanitizeError(err, h.cfg.BotToken),
		)
		h.send(ctx, bot, replyChatID, h.messages.SetupCheckFailed)
		return
	}

	permissions := inspectPermissions(member)
	if !permissions.administrator || !permissions.canInviteUsers || !permissions.canRestrictMembers {
		h.send(ctx, bot, replyChatID, h.setupResult(permissions, h.messages.SetupNotEnabled))
		return
	}
	if destination != h.cfg.OwnerUserID {
		managementChat, err := bot.GetChat(requestContext, &telegrambot.GetChatParams{ChatID: destination})
		if err != nil || managementChat == nil || (managementChat.Type != models.ChatTypeSupergroup && managementChat.Type != models.ChatTypeGroup) || managementChat.IsForum {
			h.logTelegramError("review destination check failed", destination, err)
			h.send(ctx, bot, replyChatID, h.messages.SetupManagement)
			return
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	managed := store.ManagedChat{
		ChatID: message.Chat.ID, Title: message.Chat.Title, Language: h.cfg.DefaultLanguage,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 0,
	}
	if existing, getErr := h.database.GetManagedChat(ctx, message.Chat.ID); getErr == nil {
		managed = existing
		managed.Title = message.Chat.Title
		managed.Enabled = false
	} else if !errors.Is(getErr, store.ErrManagedChatNotFound) {
		h.logger.Error("read managed chat during setup", "chat_id", message.Chat.ID, "error", getErr)
		h.send(ctx, bot, replyChatID, h.messages.SetupCheckFailed)
		return
	}
	managed.ManagementTopicID = 0
	managed.ManagementChatID = destination
	managed.ReentryTrustSeconds = 0
	probe, err := bot.SendMessage(requestContext, &telegrambot.SendMessageParams{
		ChatID: destination,
		Text:   "TAdmin setup check",
	})
	if err == nil && (probe == nil || probe.ID <= 0) {
		err = errors.New("Telegram returned no setup message ID")
	}
	if err == nil {
		_, err = bot.EditMessageText(requestContext, &telegrambot.EditMessageTextParams{
			ChatID: destination, MessageID: probe.ID,
			Text: fmt.Sprintf("TAdmin · %s (%d)", message.Chat.Title, message.Chat.ID),
		})
	}
	if err != nil {
		h.logTelegramError("review destination send/edit check failed", destination, err)
		h.send(ctx, bot, replyChatID, h.messages.SetupManagementPerm)
		return
	}
	managed.Enabled = true
	if err := h.database.SaveManagedChatSetup(ctx, managed, now); err != nil {
		h.logger.Error("enable managed chat failed", "chat_id", message.Chat.ID, "error", err)
		h.send(ctx, bot, replyChatID, h.messages.SetupCheckFailed)
		return
	}
	h.send(ctx, bot, replyChatID, h.setupResult(permissions, h.messages.SetupEnabled))
}

func (h *commandHandler) setupResult(permissions botPermissions, result string) string {
	return fmt.Sprintf(
		"%s\n%s: %s\n%s: %s\n%s: %s\n%s: %s\n%s: %s\n\n%s",
		h.messages.SetupTitle,
		h.messages.BotAdministrator, yesNo(h.messages, permissions.administrator),
		h.messages.CanInviteUsers, yesNo(h.messages, permissions.canInviteUsers),
		h.messages.CanRestrictMembers, yesNo(h.messages, permissions.canRestrictMembers),
		h.messages.CanDeleteMessages, yesNo(h.messages, permissions.canDeleteMessages),
		h.messages.JoinRequestQueries, yesNo(h.messages, h.botInfo.SupportsJoinRequestQueries), result,
	)
}

func (h *commandHandler) logTelegramError(operation string, chatID int64, err error) {
	if err == nil {
		return
	}
	h.logger.Error(operation, "chat_id", chatID, "error", sanitizeError(err, h.cfg.BotToken))
}

func (h *commandHandler) send(ctx context.Context, bot *telegrambot.Bot, chatID int64, text string) {
	requestContext, cancel := context.WithTimeout(ctx, apiRequestTimeout)
	defer cancel()
	if _, err := bot.SendMessage(requestContext, &telegrambot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	}); err != nil {
		h.logger.Error("telegram send message failed",
			"chat_id", chatID,
			"error", sanitizeError(err, h.cfg.BotToken),
		)
	}
}

func parseCommand(text string) (string, string, []string, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(text, "/") {
		return "", "", nil, false
	}

	command := strings.TrimPrefix(fields[0], "/")
	targetUsername := ""
	if at := strings.IndexByte(command, '@'); at >= 0 {
		if at == len(command)-1 {
			return "", "", nil, false
		}
		targetUsername = command[at+1:]
		command = command[:at]
	}
	if command == "" {
		return "", "", nil, false
	}
	return strings.ToLower(command), targetUsername, fields[1:], true
}

func hasLeadingBotCommand(entities []models.MessageEntity) bool {
	for _, entity := range entities {
		if entity.Offset == 0 && entity.Type == models.MessageEntityTypeBotCommand {
			return true
		}
	}
	return false
}

func inspectPermissions(member *models.ChatMember) botPermissions {
	if member == nil {
		return botPermissions{}
	}
	switch member.Type {
	case models.ChatMemberTypeOwner:
		return botPermissions{
			administrator:      true,
			canInviteUsers:     true,
			canRestrictMembers: true,
			canDeleteMessages:  true,
			canManageTopics:    true,
		}
	case models.ChatMemberTypeAdministrator:
		if member.Administrator == nil {
			return botPermissions{}
		}
		return botPermissions{
			administrator:      true,
			canInviteUsers:     member.Administrator.CanInviteUsers,
			canRestrictMembers: member.Administrator.CanRestrictMembers,
			canDeleteMessages:  member.Administrator.CanDeleteMessages,
			canManageTopics:    member.Administrator.CanManageTopics,
		}
	default:
		return botPermissions{}
	}
}

func yesNo(messages i18n.Messages, value bool) string {
	if value {
		return messages.Yes
	}
	return messages.No
}
