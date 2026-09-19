package join

import (
	"context"
	"errors"

	"github.com/go-telegram/bot/models"
)

const (
	ActionGuardOpen     = "guard_open"
	ActionGuardApprove  = "guard_approve"
	ActionGuardQueue    = "guard_queue"
	ActionPrivatePrompt = "private_prompt"
	ActionDirectPrompt  = "direct_prompt"
	ActionRestrict      = "restrict"
	ActionApprove       = "approve_request"
	ActionDecline       = "decline_request"
	ActionRestore       = "restore"
	ActionKick          = "kick"
	ActionBan           = "ban"
	ActionRecordMember  = "record_member"
)

const (
	ReasonFailed      = "verification_failed"
	ReasonExpired     = "verification_expired"
	ReasonUnreachable = "verification_unreachable"
)

var (
	ErrRequestHandled = errors.New("join request was already handled externally")
	ErrStartInvalid   = errors.New("verification link is invalid or expired")
	ErrStartIdentity  = errors.New("verification link belongs to another user")
)

type Applicant struct {
	ID        int64
	IsBot     bool
	FirstName string
	LastName  string
	Username  string
	Language  string
}

type JoinRequest struct {
	UpdateID     int64
	ChatID       int64
	ChatTitle    string
	UserChatID   int64
	QueryID      string
	Applicant    Applicant
	InviteSource string
}

type MemberUpdate struct {
	UpdateID         int64
	ChatID           int64
	ChatTitle        string
	Applicant        Applicant
	OldStatus        models.ChatMemberType
	NewStatus        models.ChatMemberType
	JoinedAtUnix     int64
	PriorPermissions string
	PriorUntil       int64
	InviteSource     string
}

type ReviewCallback struct {
	ID              string
	ActorUserID     int64
	Anonymous       bool
	ManagementChat  int64
	MessageThreadID int
	MessageID       int
	Data            string
}

type ReviewButton struct {
	Text string
	Data string
}

// Telegram contains only the operations needed by the join state machine. The
// concrete adapter continues to use github.com/go-telegram/bot for every
// standard Bot API method.
type Telegram interface {
	OpenGuardVerification(context.Context, string, string) error
	AnswerGuardQuery(context.Context, string, string) error
	SendPrivateVerification(context.Context, int64, string, string) error
	DeliverDirectVerification(context.Context, int64, int64, string, string, string) error
	ApproveJoinRequest(context.Context, int64, int64) error
	DeclineJoinRequest(context.Context, int64, int64) error
	RestrictMember(context.Context, int64, int64) error
	RestoreDefaultPermissions(context.Context, int64, int64) error
	RestorePriorPermissions(context.Context, int64, int64, string, int64) error
	KickMember(context.Context, int64, int64) error
	BanMember(context.Context, int64, int64) error
	IsAdministrator(context.Context, int64, int64) (bool, error)
	MemberStatus(context.Context, int64, int64) (models.ChatMemberType, error)
	SendReviewCard(context.Context, int64, int, string, []ReviewButton) (int, error)
	EditReviewCard(context.Context, int64, int, string) error
	AnswerCallback(context.Context, string, string, bool) error
}
