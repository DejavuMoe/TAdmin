package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	telegrambot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"tadmin/internal/admin"
	"tadmin/internal/config"
	"tadmin/internal/i18n"
	"tadmin/internal/invite"
	"tadmin/internal/join"
	"tadmin/internal/moderation"
	"tadmin/internal/store"
	"tadmin/internal/verify"
)

type commandHandler struct {
	cfg        config.Config
	database   *store.Store
	logger     *slog.Logger
	botInfo    BotInfo
	version    string
	startedAt  time.Time
	messages   i18n.Messages
	joins      *join.Coordinator
	moderation *moderation.Coordinator
	invites    *invite.Manager
	setupMu    sync.Mutex
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
	moderationCoordinator *moderation.Coordinator,
	invitationManager *invite.Manager,
) *commandHandler {
	return &commandHandler{
		cfg:        cfg,
		database:   database,
		logger:     logger,
		botInfo:    botInfo,
		version:    version,
		startedAt:  startedAt,
		messages:   i18n.For(cfg.DefaultLanguage),
		joins:      joins,
		moderation: moderationCoordinator,
		invites:    invitationManager,
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
	if len(arguments) == 1 && strings.EqualFold(arguments[0], "setup") {
		if !owner {
			h.send(ctx, bot, message.Chat.ID, h.messages.Unauthorized)
			return
		}
		h.setup(ctx, bot, message)
		return
	}
	if len(arguments) >= 1 && strings.EqualFold(arguments[0], "invite") {
		if !owner {
			h.send(ctx, bot, message.Chat.ID, h.messages.Unauthorized)
			return
		}
		if h.invites == nil || message.Chat.Type != models.ChatTypeSupergroup {
			h.send(ctx, bot, message.Chat.ID, invite.Usage(h.chatLanguage(ctx, message.Chat.ID)))
			return
		}
		h.inviteCommand(ctx, bot, message, arguments[1:])
		return
	}
	if h.moderation == nil || message.Chat.Type != models.ChatTypeSupergroup {
		h.send(ctx, bot, message.Chat.ID, h.messages.Usage)
		return
	}
	if len(arguments) == 3 && (strings.EqualFold(arguments[0], "allow") || strings.EqualFold(arguments[0], "block")) {
		if !owner {
			h.send(ctx, bot, message.Chat.ID, h.messages.Unauthorized)
			return
		}
		h.listCommand(ctx, bot, message, arguments)
		return
	}
	if len(arguments) == 2 && strings.EqualFold(arguments[0], "raid") {
		if !owner {
			h.send(ctx, bot, message.Chat.ID, h.messages.Unauthorized)
			return
		}
		h.raidCommand(ctx, bot, message, arguments[1])
		return
	}
	if len(arguments) == 2 && strings.EqualFold(arguments[0], "moderation") && strings.EqualFold(arguments[1], "status") {
		if !owner {
			h.send(ctx, bot, message.Chat.ID, h.messages.Unauthorized)
			return
		}
		h.moderationStatus(ctx, bot, message)
		return
	}
	if isDailyModerationCommand(arguments) {
		authorized, err := h.moderation.IsAdministrator(ctx, message.Chat.ID, message.From.ID)
		if err != nil || !authorized {
			if err != nil {
				h.logger.Error("moderation administrator check failed", "chat_id", message.Chat.ID, "error", sanitizeError(err, h.cfg.BotToken))
			}
			h.send(ctx, bot, message.Chat.ID, dailyUnauthorized(h.chatLanguage(ctx, message.Chat.ID)))
			return
		}
		h.manualCommand(ctx, bot, message, arguments)
		return
	}
	h.send(ctx, bot, message.Chat.ID, h.messages.Usage)
}

func (h *commandHandler) inviteCommand(ctx context.Context, bot *telegrambot.Bot, message *models.Message, arguments []string) {
	language := h.chatLanguage(ctx, message.Chat.ID)
	if len(arguments) >= 2 && strings.EqualFold(arguments[0], "create") {
		name := strings.Join(arguments[1:], " ")
		result, err := h.invites.Create(ctx,
			fmt.Sprintf("invite:create:%d:%d", message.Chat.ID, message.ID),
			message.Chat.ID, message.From.ID, name, language)
		if errors.Is(err, invite.ErrInvalidName) {
			h.send(ctx, bot, message.Chat.ID, invite.Usage(language))
			return
		}
		if err != nil {
			h.logInviteError("invite create command failed", message.Chat.ID, err)
			h.sendInviteSummary(ctx, bot, message.Chat.ID, inviteFailure(language))
			return
		}
		h.sendInviteSummary(ctx, bot, message.Chat.ID, invite.CreateResultMessage(language, result))
		return
	}
	if len(arguments) == 1 && strings.EqualFold(arguments[0], "list") {
		summaries, err := h.invites.List(ctx, message.Chat.ID, message.From.ID)
		if err != nil {
			h.logInviteError("invite list command failed", message.Chat.ID, err)
			h.send(ctx, bot, message.Chat.ID, inviteFailure(language))
			return
		}
		h.send(ctx, bot, message.Chat.ID, invite.ListMessage(language, summaries))
		return
	}
	if len(arguments) == 2 && strings.EqualFold(arguments[0], "revoke") {
		result, err := h.invites.Revoke(ctx,
			fmt.Sprintf("invite:revoke:%d:%d", message.Chat.ID, message.ID),
			message.Chat.ID, message.From.ID, strings.ToLower(arguments[1]))
		if errors.Is(err, invite.ErrInvalidID) {
			h.send(ctx, bot, message.Chat.ID, invite.Usage(language))
			return
		}
		if errors.Is(err, store.ErrInviteNotFound) {
			result = invite.OperationResult{PublicID: strings.ToLower(arguments[1]), Code: invite.ResultUnavailable}
			err = nil
		}
		if err != nil {
			h.logInviteError("invite revoke command failed", message.Chat.ID, err)
			h.sendInviteSummary(ctx, bot, message.Chat.ID, inviteFailure(language))
			return
		}
		h.sendInviteSummary(ctx, bot, message.Chat.ID, invite.RevokeResultMessage(language, result))
		return
	}
	h.send(ctx, bot, message.Chat.ID, invite.Usage(language))
}

func (h *commandHandler) logInviteError(operation string, chatID int64, err error) {
	if err != nil {
		h.logger.Error(operation, "chat_id", chatID, "error", sanitizeError(err, h.cfg.BotToken))
	}
}

func inviteFailure(language string) string {
	if language == "en" {
		return "The invite operation failed. No invite link was exposed; see the service log for details."
	}
	return "邀请操作失败，未泄露邀请链接；请查看服务日志。"
}

func (h *commandHandler) sendInviteSummary(ctx context.Context, bot *telegrambot.Bot, chatID int64, text string) {
	h.send(ctx, bot, chatID, text)
	if h.cfg.ManagementChatID == 0 {
		return
	}
	managed, err := h.database.GetManagedChat(ctx, chatID)
	if err != nil || !managed.Enabled || managed.ManagementTopicID == 0 {
		return
	}
	requestContext, cancel := context.WithTimeout(ctx, apiRequestTimeout)
	defer cancel()
	if _, err := bot.SendMessage(requestContext, &telegrambot.SendMessageParams{
		ChatID: h.cfg.ManagementChatID, MessageThreadID: managed.ManagementTopicID, Text: text,
	}); err != nil {
		h.logTelegramError("invite management summary failed", h.cfg.ManagementChatID, err)
	}
}

func (h *commandHandler) listCommand(ctx context.Context, bot *telegrambot.Bot, message *models.Message, arguments []string) {
	kind := strings.ToLower(arguments[0])
	operation := strings.ToLower(arguments[1])
	add := operation == "add"
	if !add && operation != "remove" {
		h.send(ctx, bot, message.Chat.ID, moderationUsage(h.chatLanguage(ctx, message.Chat.ID)))
		return
	}
	userID, ok := moderation.ParseUserID(arguments[2])
	if !ok || (kind == "block" && userID == h.cfg.OwnerUserID) {
		h.send(ctx, bot, message.Chat.ID, moderationUsage(h.chatLanguage(ctx, message.Chat.ID)))
		return
	}
	changed, err := h.moderation.UpdateList(ctx, fmt.Sprintf("%d:%d", message.Chat.ID, message.ID), message.Chat.ID, userID, kind, add, message.From.ID)
	if err != nil {
		h.logger.Error("moderation list command failed", "chat_id", message.Chat.ID, "error", err)
		h.send(ctx, bot, message.Chat.ID, moderationFailed(h.chatLanguage(ctx, message.Chat.ID)))
		return
	}
	h.send(ctx, bot, message.Chat.ID, listResult(h.chatLanguage(ctx, message.Chat.ID), kind, operation, userID, changed))
}

func (h *commandHandler) raidCommand(ctx context.Context, bot *telegrambot.Bot, message *models.Message, requested string) {
	mode := map[string]string{
		"auto": store.RaidModeAuto, "on": store.RaidModeForcedOn, "off": store.RaidModeForcedOff,
	}[strings.ToLower(requested)]
	if mode == "" {
		h.send(ctx, bot, message.Chat.ID, moderationUsage(h.chatLanguage(ctx, message.Chat.ID)))
		return
	}
	state, _, err := h.moderation.SetRaidMode(ctx, message.Chat.ID, mode, message.From.ID)
	if err != nil {
		h.logger.Error("Raid mode command failed", "chat_id", message.Chat.ID, "error", err)
		h.send(ctx, bot, message.Chat.ID, moderationFailed(h.chatLanguage(ctx, message.Chat.ID)))
		return
	}
	h.send(ctx, bot, message.Chat.ID, raidModeResult(h.chatLanguage(ctx, message.Chat.ID), state))
}

func (h *commandHandler) moderationStatus(ctx context.Context, bot *telegrambot.Bot, message *models.Message) {
	status, err := h.moderation.Status(ctx, message.Chat.ID)
	if err != nil {
		h.logger.Error("moderation status command failed", "chat_id", message.Chat.ID, "error", err)
		h.send(ctx, bot, message.Chat.ID, moderationFailed(h.chatLanguage(ctx, message.Chat.ID)))
		return
	}
	h.send(ctx, bot, message.Chat.ID, formatModerationStatus(h.chatLanguage(ctx, message.Chat.ID), status))
}

func (h *commandHandler) manualCommand(ctx context.Context, bot *telegrambot.Bot, message *models.Message, arguments []string) {
	command := strings.ToLower(arguments[0])
	action := map[string]string{
		"warn": store.ModerationWarn, "mute": store.ModerationMute,
		"kick": store.ModerationKick, "ban": store.ModerationBan,
	}[command]
	duration := time.Duration(0)
	targetArgument := ""
	if command == "mute" {
		if len(arguments) < 2 || len(arguments) > 3 {
			h.send(ctx, bot, message.Chat.ID, moderationUsage(h.chatLanguage(ctx, message.Chat.ID)))
			return
		}
		switch strings.ToLower(arguments[1]) {
		case "10m":
			duration = 10 * time.Minute
		case "24h":
			duration = 24 * time.Hour
		default:
			h.send(ctx, bot, message.Chat.ID, moderationUsage(h.chatLanguage(ctx, message.Chat.ID)))
			return
		}
		if len(arguments) == 3 {
			targetArgument = arguments[2]
		}
	} else {
		if len(arguments) > 2 {
			h.send(ctx, bot, message.Chat.ID, moderationUsage(h.chatLanguage(ctx, message.Chat.ID)))
			return
		}
		if len(arguments) == 2 {
			targetArgument = arguments[1]
		}
	}
	targetID, ok := commandTarget(message, targetArgument)
	if !ok || targetID == h.cfg.OwnerUserID {
		h.send(ctx, bot, message.Chat.ID, moderationUsage(h.chatLanguage(ctx, message.Chat.ID)))
		return
	}
	created, err := h.moderation.ScheduleManual(ctx, message.ID, message.Chat.ID, targetID, message.From.ID, action, duration)
	if err != nil {
		h.logger.Error("manual moderation command failed", "chat_id", message.Chat.ID, "error", err)
		h.send(ctx, bot, message.Chat.ID, moderationFailed(h.chatLanguage(ctx, message.Chat.ID)))
		return
	}
	h.send(ctx, bot, message.Chat.ID, manualQueued(h.chatLanguage(ctx, message.Chat.ID), action, targetID, created))
}

func commandTarget(message *models.Message, explicit string) (int64, bool) {
	if explicit != "" {
		return moderation.ParseUserID(explicit)
	}
	if message.ReplyToMessage == nil || message.ReplyToMessage.SenderChat != nil || message.ReplyToMessage.From == nil {
		return 0, false
	}
	if message.ReplyToMessage.From.ID == 1087968824 {
		return 0, false
	}
	return message.ReplyToMessage.From.ID, message.ReplyToMessage.From.ID > 0
}

func isDailyModerationCommand(arguments []string) bool {
	if len(arguments) == 0 {
		return false
	}
	switch strings.ToLower(arguments[0]) {
	case "warn", "mute", "kick", "ban":
		return true
	default:
		return false
	}
}

func (h *commandHandler) chatLanguage(ctx context.Context, chatID int64) string {
	chat, err := h.database.GetManagedChat(ctx, chatID)
	if err == nil {
		return chat.Language
	}
	return h.cfg.DefaultLanguage
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
	h.send(ctx, bot, chatID, text)
}

func (h *commandHandler) setup(ctx context.Context, bot *telegrambot.Bot, message *models.Message) {
	if message.Chat.Type != models.ChatTypeSupergroup {
		h.send(ctx, bot, message.Chat.ID, h.messages.SetupSupergroupOnly)
		return
	}
	h.setupMu.Lock()
	defer h.setupMu.Unlock()

	if _, err := verify.VerificationURL(h.cfg.PublicBaseURL, strings.Repeat("a", 32)); err != nil {
		h.send(ctx, bot, message.Chat.ID, h.messages.SetupPublicURL)
		return
	}
	if h.cfg.ManagementChatID == 0 {
		h.send(ctx, bot, message.Chat.ID, h.messages.SetupManagement)
		return
	}

	requestContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	member, err := bot.GetChatMember(requestContext, &telegrambot.GetChatMemberParams{
		ChatID: message.Chat.ID,
		UserID: h.botInfo.ID,
	})
	if err != nil {
		h.logger.Error("telegram permission check failed",
			"chat_id", message.Chat.ID,
			"error", sanitizeError(err, h.cfg.BotToken),
		)
		h.send(ctx, bot, message.Chat.ID, h.messages.SetupCheckFailed)
		return
	}

	permissions := inspectPermissions(member)
	if !permissions.administrator || !permissions.canInviteUsers || !permissions.canRestrictMembers || !permissions.canDeleteMessages {
		h.send(ctx, bot, message.Chat.ID, h.setupResult(permissions, h.messages.SetupNotEnabled))
		return
	}
	managementChat, err := bot.GetChat(requestContext, &telegrambot.GetChatParams{ChatID: h.cfg.ManagementChatID})
	if err != nil || managementChat.Type != models.ChatTypeSupergroup || !managementChat.IsForum {
		h.logTelegramError("management chat check failed", h.cfg.ManagementChatID, err)
		h.send(ctx, bot, message.Chat.ID, h.messages.SetupManagement)
		return
	}
	managementMember, err := bot.GetChatMember(requestContext, &telegrambot.GetChatMemberParams{
		ChatID: h.cfg.ManagementChatID, UserID: h.botInfo.ID,
	})
	if err != nil {
		h.logTelegramError("management permission check failed", h.cfg.ManagementChatID, err)
		h.send(ctx, bot, message.Chat.ID, h.messages.SetupManagementPerm)
		return
	}
	managementPermissions := inspectPermissions(managementMember)
	if !managementPermissions.administrator || !managementPermissions.canManageTopics {
		h.send(ctx, bot, message.Chat.ID, h.messages.SetupManagementPerm)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	managed := store.ManagedChat{
		ChatID: message.Chat.ID, Title: message.Chat.Title, Language: h.cfg.DefaultLanguage,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 30 * 24 * 60 * 60,
	}
	if existing, getErr := h.database.GetManagedChat(ctx, message.Chat.ID); getErr == nil {
		managed = existing
		managed.Title = message.Chat.Title
		managed.Enabled = false
	} else if !errors.Is(getErr, store.ErrManagedChatNotFound) {
		h.logger.Error("read managed chat during setup", "chat_id", message.Chat.ID, "error", getErr)
		h.send(ctx, bot, message.Chat.ID, h.messages.SetupCheckFailed)
		return
	}
	if managed.ManagementTopicID == 0 {
		topicName := "TAdmin · " + message.Chat.Title
		if runes := []rune(topicName); len(runes) > 128 {
			topicName = string(runes[:128])
		}
		topic, createErr := bot.CreateForumTopic(requestContext, &telegrambot.CreateForumTopicParams{
			ChatID: h.cfg.ManagementChatID, Name: topicName,
		})
		if createErr != nil {
			h.logTelegramError("create management topic failed", h.cfg.ManagementChatID, createErr)
			h.send(ctx, bot, message.Chat.ID, h.messages.SetupManagementPerm)
			return
		}
		managed.ManagementTopicID = topic.MessageThreadID
		if err := h.database.SaveManagedChatSetup(ctx, managed, now); err != nil {
			h.logger.Error("save management topic failed", "chat_id", message.Chat.ID, "error", err)
			h.send(ctx, bot, message.Chat.ID, h.messages.SetupCheckFailed)
			return
		}
	}
	probe, err := bot.SendMessage(requestContext, &telegrambot.SendMessageParams{
		ChatID: h.cfg.ManagementChatID, MessageThreadID: managed.ManagementTopicID,
		Text: "TAdmin setup check",
	})
	if err == nil {
		_, err = bot.EditMessageText(requestContext, &telegrambot.EditMessageTextParams{
			ChatID: h.cfg.ManagementChatID, MessageID: probe.ID,
			Text: "TAdmin · " + message.Chat.Title,
		})
	}
	if err != nil {
		h.logTelegramError("management topic send/edit check failed", h.cfg.ManagementChatID, err)
		h.send(ctx, bot, message.Chat.ID, h.messages.SetupManagementPerm)
		return
	}
	managed.Enabled = true
	if err := h.database.SaveManagedChatSetup(ctx, managed, now); err != nil {
		h.logger.Error("enable managed chat failed", "chat_id", message.Chat.ID, "error", err)
		h.send(ctx, bot, message.Chat.ID, h.messages.SetupCheckFailed)
		return
	}
	h.send(ctx, bot, message.Chat.ID, h.setupResult(permissions, h.messages.SetupEnabled))
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

func moderationUsage(language string) string {
	if language == "en" {
		return "Usage: /tadmin allow|block add|remove <user_id>; /tadmin raid auto|on|off; /tadmin moderation status; or reply with /tadmin warn|mute 10m|mute 24h|kick|ban."
	}
	return "用法：/tadmin allow|block add|remove <user_id>；/tadmin raid auto|on|off；/tadmin moderation status；或回复消息执行 /tadmin warn|mute 10m|mute 24h|kick|ban。"
}

func dailyUnauthorized(language string) string {
	if language == "en" {
		return "Access denied: only the TAdmin owner or a current group administrator may use this action."
	}
	return "拒绝访问：仅 TAdmin 全局所有者或本群当前管理员可执行此操作。"
}

func moderationFailed(language string) string {
	if language == "en" {
		return "The moderation operation failed. See the service log for details."
	}
	return "群管操作失败，请查看服务日志。"
}

func listResult(language, kind, operation string, userID int64, changed bool) string {
	if language == "en" {
		if !changed {
			return fmt.Sprintf("No change: user %d already has the requested %s state.", userID, kind)
		}
		return fmt.Sprintf("Moderation %s %s for user %d.", kind, map[string]string{"add": "added", "remove": "removed"}[operation], userID)
	}
	if !changed {
		return fmt.Sprintf("未更改：用户 %d 已处于所请求的 %s 状态。", userID, kind)
	}
	verb := map[string]string{"add": "已加入", "remove": "已移出"}[operation]
	return fmt.Sprintf("用户 %d %s %s。", userID, verb, kind)
}

func raidModeResult(language string, state store.RaidState) string {
	if language == "en" {
		return fmt.Sprintf("Raid mode: %s; active: %t.", state.Mode, state.Active)
	}
	return fmt.Sprintf("Raid 模式：%s；当前启用：%t。", state.Mode, state.Active)
}

func formatModerationStatus(language string, status store.ModerationStatus) string {
	entered, signal := "-", "-"
	if status.EnteredAt != nil {
		entered = status.EnteredAt.UTC().Format(time.RFC3339)
	}
	if status.LastSignalAt != nil {
		signal = status.LastSignalAt.UTC().Format(time.RFC3339)
	}
	if language == "en" {
		return fmt.Sprintf("TAdmin moderation status\nRaid mode: %s\nRaid active: %t\nEntered: %s\nLast signal: %s\nReason: %s\nJoins/60s: %d\nVerification failures/5m: %d\nCoordinated repeats/5m: %d\nPunished recent members/5m: %d\nPending actions: %d",
			status.Mode, status.Active, entered, signal, status.LastReason,
			status.JoinsLastMinute, status.FailuresLastFiveMin, status.CoordinatedLastFiveMin,
			status.PunishedLastFiveMin, status.PendingActions)
	}
	return fmt.Sprintf("TAdmin 群管状态\nRaid 模式：%s\nRaid 启用：%t\n进入时间：%s\n最近信号：%s\n原因：%s\n入群/60秒：%d\n验证失败/5分钟：%d\n协同重复/5分钟：%d\n近期成员处罚/5分钟：%d\n待处理动作：%d",
		status.Mode, status.Active, entered, signal, status.LastReason,
		status.JoinsLastMinute, status.FailuresLastFiveMin, status.CoordinatedLastFiveMin,
		status.PunishedLastFiveMin, status.PendingActions)
}

func manualQueued(language, action string, userID int64, created bool) string {
	if language == "en" {
		if !created {
			return "This moderation action has already been queued."
		}
		return fmt.Sprintf("Queued %s for user %d.", action, userID)
	}
	if !created {
		return "此群管动作已进入队列。"
	}
	return fmt.Sprintf("已为用户 %d 排队执行 %s。", userID, action)
}
