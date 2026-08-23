package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"

	"tadmin/internal/join"
)

type extendedUpdate struct {
	Update  *models.Update
	QueryID string
}

func decodeExtendedUpdate(data []byte) (extendedUpdate, error) {
	var update models.Update
	if err := json.Unmarshal(data, &update); err != nil {
		return extendedUpdate{}, fmt.Errorf("decode Telegram update: %w", err)
	}
	var extra struct {
		ChatJoinRequest *struct {
			QueryID string `json:"query_id"`
		} `json:"chat_join_request"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return extendedUpdate{}, fmt.Errorf("decode extended Telegram update: %w", err)
	}
	result := extendedUpdate{Update: &update}
	if extra.ChatJoinRequest != nil {
		result.QueryID = extra.ChatJoinRequest.QueryID
	}
	return result, nil
}

func (c *Client) poll(ctx context.Context) {
	var offset int64
	var retry time.Duration
	for {
		if retry > 0 {
			timer := time.NewTimer(retry)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		var rawUpdates []json.RawMessage
		err := c.raw.call(ctx, "getUpdates", struct {
			Offset         int64    `json:"offset,omitempty"`
			Timeout        int      `json:"timeout"`
			AllowedUpdates []string `json:"allowed_updates"`
		}{Offset: offset, Timeout: 59, AllowedUpdates: []string(allowedUpdates)}, &rawUpdates)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}
			c.logger.Error("telegram polling error", "error", sanitizeError(err, c.token))
			retry = nextRetry(retry)
			continue
		}
		retry = 0
		for _, raw := range rawUpdates {
			update, err := decodeExtendedUpdate(raw)
			if err != nil {
				c.logger.Error("telegram update decode failed", "error", err)
				continue
			}
			if err := c.handleExtendedUpdate(ctx, update); err != nil {
				c.logger.Error("telegram update handling failed", "update_id", update.Update.ID, "error", sanitizeError(err, c.token))
				retry = time.Second
				break
			}
			if update.Update.ID >= offset {
				offset = update.Update.ID + 1
			}
		}
	}
}

func (c *Client) handleExtendedUpdate(ctx context.Context, envelope extendedUpdate) error {
	update := envelope.Update
	if update.Message != nil {
		if err := c.moderation.HandleMessage(ctx, update.ID, update.Message, false); err != nil {
			return err
		}
	}
	if update.EditedMessage != nil {
		if err := c.moderation.HandleMessage(ctx, update.ID, update.EditedMessage, true); err != nil {
			return err
		}
	}
	if update.ChatJoinRequest != nil {
		request := update.ChatJoinRequest
		err := c.coordinator.HandleJoinRequest(ctx, join.JoinRequest{
			UpdateID: update.ID, ChatID: request.Chat.ID, ChatTitle: request.Chat.Title,
			UserChatID: request.UserChatID, QueryID: envelope.QueryID,
			Applicant: join.Applicant{
				ID: request.From.ID, IsBot: request.From.IsBot, FirstName: request.From.FirstName,
				LastName: request.From.LastName, Username: request.From.Username, Language: request.From.LanguageCode,
			},
			InviteSource: inviteSource(request.InviteLink),
		})
		if err != nil {
			return err
		}
	}
	if update.ChatMember != nil {
		change := update.ChatMember
		user := memberUser(change.NewChatMember)
		if user == nil {
			user = memberUser(change.OldChatMember)
		}
		if user != nil {
			err := c.coordinator.HandleMemberUpdate(ctx, join.MemberUpdate{
				UpdateID: update.ID, ChatID: change.Chat.ID, ChatTitle: change.Chat.Title,
				Applicant: join.Applicant{ID: user.ID, IsBot: user.IsBot, FirstName: user.FirstName, LastName: user.LastName, Username: user.Username, Language: user.LanguageCode},
				OldStatus: change.OldChatMember.Type, NewStatus: change.NewChatMember.Type,
				JoinedAtUnix: int64(change.Date), InviteSource: inviteSource(change.InviteLink),
			})
			if err != nil {
				return err
			}
		}
	}
	if update.CallbackQuery != nil {
		callback := update.CallbackQuery
		message := callback.Message.Message
		if message != nil {
			err := c.coordinator.HandleReviewCallback(ctx, join.ReviewCallback{
				ID: callback.ID, ActorUserID: callback.From.ID,
				Anonymous:      callback.From.ID == 1087968824,
				ManagementChat: message.Chat.ID, MessageThreadID: message.MessageThreadID,
				MessageID: message.ID, Data: callback.Data,
			})
			if err != nil {
				return err
			}
		}
	}
	c.bot.ProcessUpdate(ctx, update)
	return nil
}

func nextRetry(current time.Duration) time.Duration {
	if current == 0 {
		return 100 * time.Millisecond
	}
	current *= 2
	if current > 5*time.Second {
		return 5 * time.Second
	}
	return current
}

func inviteSource(link *models.ChatInviteLink) string {
	if link == nil {
		return ""
	}
	if link.Name != "" {
		name := strings.Join(strings.Fields(link.Name), " ")
		lower := strings.ToLower(name)
		if strings.Contains(lower, "://") || strings.Contains(lower, "t.me/") || strings.Contains(lower, "joinchat") {
			return "named"
		}
		if runes := []rune(name); len(runes) > 64 {
			name = string(runes[:64])
		}
		return "name:" + name
	}
	if link.Creator.ID > 0 {
		return "creator:" + strconv.FormatInt(link.Creator.ID, 10)
	}
	if link.IsPrimary {
		return "primary"
	}
	return "unnamed"
}

func memberUser(member models.ChatMember) *models.User {
	switch member.Type {
	case models.ChatMemberTypeOwner:
		if member.Owner != nil {
			return member.Owner.User
		}
	case models.ChatMemberTypeAdministrator:
		if member.Administrator != nil {
			return &member.Administrator.User
		}
	case models.ChatMemberTypeMember:
		if member.Member != nil {
			return member.Member.User
		}
	case models.ChatMemberTypeRestricted:
		if member.Restricted != nil {
			return member.Restricted.User
		}
	case models.ChatMemberTypeLeft:
		if member.Left != nil {
			return member.Left.User
		}
	case models.ChatMemberTypeBanned:
		if member.Banned != nil {
			return member.Banned.User
		}
	}
	return nil
}
