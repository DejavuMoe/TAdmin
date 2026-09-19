package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	telegrambot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"tadmin/internal/invite"
	"tadmin/internal/join"
)

type gateway struct {
	bot   *telegrambot.Bot
	raw   *rawBotAPI
	token string
}

func (g *gateway) SendInvitePreflight(ctx context.Context, ownerUserID int64, text string) error {
	_, err := g.bot.SendMessage(ctx, &telegrambot.SendMessageParams{ChatID: ownerUserID, Text: text})
	return g.clean(err)
}

func (g *gateway) CreateJoinRequestInvite(ctx context.Context, chatID int64, name string) (invite.TelegramLink, error) {
	link, err := g.bot.CreateChatInviteLink(ctx, &telegrambot.CreateChatInviteLinkParams{
		ChatID: chatID, Name: name, CreatesJoinRequest: true,
	})
	if err != nil {
		return invite.TelegramLink{}, g.inviteError(err)
	}
	if link == nil {
		return invite.TelegramLink{}, errors.New("Telegram returned an empty invite link result")
	}
	return invite.TelegramLink{URL: link.InviteLink, CreatesJoinRequest: link.CreatesJoinRequest}, nil
}

func (g *gateway) SendInviteSecret(ctx context.Context, ownerUserID int64, text, inviteURL string) error {
	_, err := g.bot.SendMessage(ctx, &telegrambot.SendMessageParams{
		ChatID: ownerUserID, Text: text + "\n" + inviteURL,
	})
	return g.clean(err)
}

func (g *gateway) RevokeInvite(ctx context.Context, chatID int64, inviteURL string) error {
	_, err := g.bot.RevokeChatInviteLink(ctx, &telegrambot.RevokeChatInviteLinkParams{
		ChatID: chatID, InviteLink: inviteURL,
	})
	if err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "invite link is revoked") || strings.Contains(lower, "invite link already revoked") ||
			strings.Contains(lower, "invite_hash_expired") || strings.Contains(lower, "invite hash expired") {
			return nil
		}
	}
	return g.inviteError(err)
}

func (g *gateway) OpenGuardVerification(ctx context.Context, queryID, webAppURL string) error {
	return g.clean(g.raw.sendChatJoinRequestWebApp(ctx, queryID, webAppURL))
}

func (g *gateway) AnswerGuardQuery(ctx context.Context, queryID, result string) error {
	return g.clean(g.raw.answerChatJoinRequestQuery(ctx, queryID, result))
}

func (g *gateway) SendPrivateVerification(ctx context.Context, chatID int64, text, webAppURL string) error {
	_, err := g.bot.SendMessage(ctx, &telegrambot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
		ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{
			Text:   verificationButtonText(text),
			WebApp: &models.WebAppInfo{URL: webAppURL},
		}}}},
	})
	return g.clean(err)
}

func (g *gateway) DeliverDirectVerification(ctx context.Context, chatID, userID int64, text, webAppURL, deepLink string) error {
	privateErr := g.SendPrivateVerification(ctx, userID, text, webAppURL)
	if privateErr == nil {
		return nil
	}
	_, groupErr := g.bot.SendMessage(ctx, &telegrambot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
		ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{
			Text: verificationButtonText(text), URL: deepLink,
		}}}},
	})
	if groupErr == nil {
		return nil
	}
	return errors.Join(privateErr, g.clean(groupErr))
}

func (g *gateway) ApproveJoinRequest(ctx context.Context, chatID, userID int64) error {
	ok, err := g.bot.ApproveChatJoinRequest(ctx, &telegrambot.ApproveChatJoinRequestParams{ChatID: chatID, UserID: userID})
	if err != nil {
		member, checkErr := g.bot.GetChatMember(ctx, &telegrambot.GetChatMemberParams{ChatID: chatID, UserID: userID})
		if checkErr == nil && member != nil && isMember(membershipStatus(*member)) {
			return nil
		}
	}
	if err != nil && errors.Is(err, telegrambot.ErrorBadRequest) && strings.Contains(err.Error(), "HIDE_REQUESTER_MISSING") {
		return join.ErrRequestHandled
	}
	return g.booleanResult(ok, err)
}

func (g *gateway) DeclineJoinRequest(ctx context.Context, chatID, userID int64) error {
	ok, err := g.bot.DeclineChatJoinRequest(ctx, &telegrambot.DeclineChatJoinRequestParams{ChatID: chatID, UserID: userID})
	if err != nil && errors.Is(err, telegrambot.ErrorBadRequest) && strings.Contains(err.Error(), "HIDE_REQUESTER_MISSING") {
		return join.ErrRequestHandled
	}
	return g.booleanResult(ok, err)
}

func (g *gateway) RestrictMember(ctx context.Context, chatID, userID int64) error {
	ok, err := g.bot.RestrictChatMember(ctx, &telegrambot.RestrictChatMemberParams{
		ChatID: chatID, UserID: userID, Permissions: &models.ChatPermissions{}, UseIndependentChatPermissions: true,
	})
	return g.booleanResult(ok, err)
}

func permissionMap(p *models.ChatPermissions) map[string]bool {
	return map[string]bool{
		"can_send_messages": p.CanSendMessages, "can_send_audios": p.CanSendAudios, "can_send_documents": p.CanSendDocuments,
		"can_send_photos": p.CanSendPhotos, "can_send_videos": p.CanSendVideos, "can_send_video_notes": p.CanSendVideoNotes,
		"can_send_voice_notes": p.CanSendVoiceNotes, "can_send_polls": p.CanSendPolls, "can_send_other_messages": p.CanSendOtherMessages,
		"can_add_web_page_previews": p.CanAddWebPagePreviews, "can_change_info": p.CanChangeInfo, "can_invite_users": p.CanInviteUsers,
		"can_pin_messages": p.CanPinMessages, "can_manage_topics": p.CanManageTopics, "can_edit_tag": p.CanEditTag, "can_react_to_messages": p.CanReactToMessages,
	}
}

func (g *gateway) restorePermissions(ctx context.Context, chatID, userID int64, permissions map[string]bool, until int64) error {
	if g.raw == nil {
		return errors.New("raw permission adapter is unavailable")
	}
	var ok bool
	err := g.raw.call(ctx, "restrictChatMember", map[string]any{"chat_id": chatID, "user_id": userID, "permissions": permissions, "use_independent_chat_permissions": true, "until_date": until}, &ok)
	return g.booleanResult(ok, err)
}

func (g *gateway) RestoreDefaultPermissions(ctx context.Context, chatID, userID int64) error {
	permissions := permissionMap(&models.ChatPermissions{})
	// All true clears individual restrictions. Telegram's current and future
	// default group permissions still apply to the resulting ordinary member.
	for key := range permissions {
		permissions[key] = true
	}
	return g.restorePermissions(ctx, chatID, userID, permissions, 0)
}

func (g *gateway) RestorePriorPermissions(ctx context.Context, chatID, userID int64, prior string, until int64) error {
	now := time.Now().Unix()
	if until > 0 && until <= now {
		return g.RestoreDefaultPermissions(ctx, chatID, userID)
	}
	// Keep margin for network transit around Telegram's 30-second cutoff.
	if until > 0 && until-now < 60 {
		return errors.New("prior restrictions expire shortly; restoration will retry")
	}
	var rights models.ChatPermissions
	if err := json.Unmarshal([]byte(prior), &rights); err != nil {
		return errors.New("invalid saved member permissions")
	}
	return g.restorePermissions(ctx, chatID, userID, permissionMap(&rights), until)
}

func (g *gateway) KickMember(ctx context.Context, chatID, userID int64) error {
	// The documented default removes a current member while allowing re-entry,
	// without a two-step permanent-ban window or deletion of their message history.
	ok, err := g.bot.UnbanChatMember(ctx, &telegrambot.UnbanChatMemberParams{ChatID: chatID, UserID: userID})
	return g.booleanResult(ok, err)
}

func (g *gateway) BanMember(ctx context.Context, chatID, userID int64) error {
	ok, err := g.bot.BanChatMember(ctx, &telegrambot.BanChatMemberParams{ChatID: chatID, UserID: userID, RevokeMessages: true})
	return g.booleanResult(ok, err)
}

func (g *gateway) DeleteModerationMessage(ctx context.Context, chatID int64, messageID int) error {
	_, err := g.bot.DeleteMessage(ctx, &telegrambot.DeleteMessageParams{ChatID: chatID, MessageID: messageID})
	if err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "message to delete not found") || strings.Contains(lower, "message not found") {
			return nil
		}
	}
	return g.clean(err)
}

func (g *gateway) SendModerationNotice(ctx context.Context, chatID int64, text string) error {
	_, err := g.bot.SendMessage(ctx, &telegrambot.SendMessageParams{
		ChatID: chatID, Text: text, DisableNotification: true,
	})
	return g.clean(err)
}

func (g *gateway) RestrictMemberUntil(ctx context.Context, chatID, userID int64, until time.Time) error {
	_, err := g.bot.RestrictChatMember(ctx, &telegrambot.RestrictChatMemberParams{
		ChatID: chatID, UserID: userID, Permissions: &models.ChatPermissions{},
		UseIndependentChatPermissions: true, UntilDate: int(until.Unix()),
	})
	return g.clean(err)
}

func (g *gateway) SendManagementLog(ctx context.Context, managementChatID int64, topicID int, text string) error {
	_, err := g.bot.SendMessage(ctx, &telegrambot.SendMessageParams{
		ChatID: managementChatID, MessageThreadID: topicID, Text: text,
		DisableNotification: true,
	})
	return g.clean(err)
}

func (g *gateway) IsAdministrator(ctx context.Context, chatID, userID int64) (bool, error) {
	member, err := g.bot.GetChatMember(ctx, &telegrambot.GetChatMemberParams{ChatID: chatID, UserID: userID})
	if err != nil {
		return false, g.clean(err)
	}
	return member != nil && (member.Type == models.ChatMemberTypeOwner || member.Type == models.ChatMemberTypeAdministrator), nil
}

func (g *gateway) SendReviewCard(ctx context.Context, managementChatID int64, topicID int, text string, buttons []join.ReviewButton) (int, error) {
	keyboard := make([][]models.InlineKeyboardButton, 0, 2)
	for index := 0; index < len(buttons); index += 2 {
		row := make([]models.InlineKeyboardButton, 0, 2)
		for offset := 0; offset < 2 && index+offset < len(buttons); offset++ {
			button := buttons[index+offset]
			row = append(row, models.InlineKeyboardButton{Text: button.Text, CallbackData: button.Data})
		}
		keyboard = append(keyboard, row)
	}
	message, err := g.bot.SendMessage(ctx, &telegrambot.SendMessageParams{
		ChatID: managementChatID, Text: text,
		ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: keyboard},
	})
	if err != nil {
		return 0, g.clean(err)
	}
	if message == nil || message.ID <= 0 {
		return 0, errors.New("Telegram returned no review message ID")
	}
	return message.ID, nil
}

func (g *gateway) EditReviewCard(ctx context.Context, managementChatID int64, messageID int, text string) error {
	_, err := g.bot.EditMessageText(ctx, &telegrambot.EditMessageTextParams{
		ChatID: managementChatID, MessageID: messageID, Text: text,
		ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}},
	})
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return g.clean(err)
}

func (g *gateway) AnswerCallback(ctx context.Context, callbackID, text string, alert bool) error {
	ok, err := g.bot.AnswerCallbackQuery(ctx, &telegrambot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackID, Text: text, ShowAlert: alert,
	})
	return g.booleanResult(ok, err)
}

func (g *gateway) clean(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(sanitizeError(err, g.token))
}

func (g *gateway) inviteError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if errors.Is(err, telegrambot.ErrorForbidden) || errors.Is(err, telegrambot.ErrorUnauthorized) ||
		strings.Contains(lower, "chat_admin_required") || strings.Contains(lower, "not enough rights") ||
		strings.Contains(lower, "not enough privileges") || strings.Contains(lower, "administrator rights") ||
		strings.Contains(lower, "can_invite_users") {
		return invite.ErrTelegramPermission
	}
	if errors.Is(err, telegrambot.ErrorBadRequest) || errors.Is(err, telegrambot.ErrorNotFound) ||
		errors.Is(err, telegrambot.ErrorConflict) || telegrambot.IsTooManyRequestsError(err) {
		return invite.ErrTelegramRejected
	}
	return g.clean(err)
}

func verificationButtonText(prompt string) string {
	if len(prompt) > 0 && prompt[0] > 127 {
		return "开始验证"
	}
	return "Open verification"
}

func isMember(status models.ChatMemberType) bool {
	return status == models.ChatMemberTypeOwner || status == models.ChatMemberTypeAdministrator ||
		status == models.ChatMemberTypeMember || status == models.ChatMemberTypeRestricted
}

func (g *gateway) MemberStatus(ctx context.Context, chatID, userID int64) (models.ChatMemberType, error) {
	member, err := g.bot.GetChatMember(ctx, &telegrambot.GetChatMemberParams{ChatID: chatID, UserID: userID})
	if err != nil {
		return "", g.clean(err)
	}
	if member == nil {
		return "", errors.New("Telegram returned no member status")
	}
	return membershipStatus(*member), nil
}

func (g *gateway) booleanResult(ok bool, err error) error {
	if err != nil {
		return g.clean(err)
	}
	if !ok {
		return errors.New("Telegram returned false for an action")
	}
	return nil
}
