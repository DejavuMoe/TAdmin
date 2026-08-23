package invite

import (
	"context"
	"errors"
)

const (
	ResultDelivered             = "delivered"
	ResultExisting              = "existing"
	ResultPreflightFailed       = "preflight_failed"
	ResultCreateRejected        = "create_rejected"
	ResultCreateIndeterminate   = "create_indeterminate"
	ResultDeliveryFailedRevoked = "delivery_failed_revoked"
	ResultRevocationPending     = "revocation_pending"
	ResultRevoked               = "revoked"
	ResultAlreadyRevoked        = "already_revoked"
	ResultDecryptFailed         = "decrypt_failed"
	ResultPermissionDenied      = "permission_denied"
	ResultUnavailable           = "unavailable"
)

var (
	ErrUnauthorized       = errors.New("invite management is restricted to the owner")
	ErrInvalidName        = errors.New("invalid invite link name")
	ErrInvalidID          = errors.New("invalid invite link identifier")
	ErrCiphertext         = errors.New("invite link ciphertext is invalid or uses a different bot token")
	ErrInviteURL          = errors.New("Telegram returned an invalid invite link")
	ErrTelegramPermission = errors.New("Telegram rejected the invite operation because bot permissions are insufficient")
	ErrTelegramRejected   = errors.New("Telegram rejected the invite operation")
)

type TelegramLink struct {
	URL                string
	CreatesJoinRequest bool
}

type Telegram interface {
	SendInvitePreflight(context.Context, int64, string) error
	CreateJoinRequestInvite(context.Context, int64, string) (TelegramLink, error)
	SendInviteSecret(context.Context, int64, string, string) error
	RevokeInvite(context.Context, int64, string) error
}

type OperationResult struct {
	PublicID string
	Name     string
	Status   string
	Code     string
}
