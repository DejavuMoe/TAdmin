package moderation

import (
	"context"
	"time"
)

// Telegram contains only the standard Bot API operations used by stage 4.
// The concrete adapter uses github.com/go-telegram/bot for every method.
type Telegram interface {
	DeleteModerationMessage(context.Context, int64, int) error
	SendModerationNotice(context.Context, int64, string) error
	RestrictMemberUntil(context.Context, int64, int64, time.Time) error
	KickMember(context.Context, int64, int64) error
	BanMember(context.Context, int64, int64) error
	IsAdministrator(context.Context, int64, int64) (bool, error)
	SendManagementLog(context.Context, int64, int, string) error
}
