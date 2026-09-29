package userbot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tgerr"
)

// Sentinel errors returned by Manager methods. Errors derived from Telegram
// RPC failures wrap both the sentinel and the original error, so callers can
// use errors.Is with the sentinel and still log the underlying cause.
var (
	// ErrNotReady means the MTProto connection is not established or the
	// account is not authorized for the requested operation.
	ErrNotReady = errors.New("userbot: not ready")
	// ErrAlreadyLoggedIn means a login flow was requested while the account
	// is already authorized.
	ErrAlreadyLoggedIn = errors.New("userbot: already logged in")
	// ErrLoginInProgress means another step of the current login flow is
	// still being processed.
	ErrLoginInProgress = errors.New("userbot: login step in progress")
	// ErrNoLogin means there is no login flow awaiting the submitted step.
	ErrNoLogin = errors.New("userbot: no matching login flow")
	// ErrLoginCancelled means the login flow was cancelled or replaced.
	ErrLoginCancelled = errors.New("userbot: login cancelled")
	// ErrInvalidCode means the login code was rejected.
	ErrInvalidCode = errors.New("userbot: invalid login code")
	// ErrCodeExpired means the login code expired; start a new phone login.
	ErrCodeExpired = errors.New("userbot: login code expired")
	// ErrPasswordInvalid means the two-step verification password was rejected.
	ErrPasswordInvalid = errors.New("userbot: invalid password")
	// ErrPhoneInvalid means the phone number was rejected or malformed.
	ErrPhoneInvalid = errors.New("userbot: invalid phone number")
	// ErrPhoneBanned means the phone number is banned from Telegram.
	ErrPhoneBanned = errors.New("userbot: phone number banned")
	// ErrSignUpRequired means the phone number has no Telegram account;
	// registering new accounts is not supported.
	ErrSignUpRequired = errors.New("userbot: phone number is not registered")
	// ErrTimeout means the operation did not complete in time.
	ErrTimeout = errors.New("userbot: timeout")
	// ErrAuthLost means Telegram revoked the session; the account must log
	// in again.
	ErrAuthLost = errors.New("userbot: session revoked")
)

// FloodWaitError reports that Telegram asked to wait before retrying
// (FLOOD_WAIT_X, FLOOD_PREMIUM_WAIT_X, SLOWMODE_WAIT_X).
type FloodWaitError struct {
	Wait time.Duration
}

// Error implements error.
func (e *FloodWaitError) Error() string {
	return fmt.Sprintf("userbot: flood wait %s", e.Wait)
}

// errPasswordNeeded is an internal marker for SESSION_PASSWORD_NEEDED.
var errPasswordNeeded = errors.New("userbot: password needed")

// authLostTypes are RPC error types meaning the stored authorization is gone.
var authLostTypes = []string{
	"AUTH_KEY_UNREGISTERED",
	"AUTH_KEY_INVALID",
	"AUTH_KEY_PERM_EMPTY",
	"SESSION_REVOKED",
	"SESSION_EXPIRED",
	"USER_DEACTIVATED",
	"USER_DEACTIVATED_BAN",
}

// isAuthLost reports whether err means the authorization was revoked.
func isAuthLost(err error) bool {
	return tgerr.Is(err, authLostTypes...)
}

// isPasswordNeeded reports whether err means two-step verification is required.
func isPasswordNeeded(err error) bool {
	return errors.Is(err, errPasswordNeeded) ||
		errors.Is(err, auth.ErrPasswordAuthNeeded) ||
		tgerr.Is(err, "SESSION_PASSWORD_NEEDED")
}

// floodWait extracts the wait duration from flood-control errors.
func floodWait(err error) (time.Duration, bool) {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return d, true
	}
	if rpcErr, ok := tgerr.AsType(err, "SLOWMODE_WAIT"); ok {
		return time.Duration(rpcErr.Argument) * time.Second, true
	}
	return 0, false
}

// wrap returns an error matching both sentinel and cause.
func wrap(sentinel, cause error) error {
	return fmt.Errorf("%w: %w", sentinel, cause)
}

// mapError translates gotd/Telegram errors into package errors. It is pure;
// Manager.observe adds the state side effects.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if d, ok := floodWait(err); ok {
		return &FloodWaitError{Wait: d}
	}
	switch {
	case isPasswordNeeded(err):
		return errPasswordNeeded
	case errors.Is(err, auth.ErrPasswordInvalid), tgerr.Is(err, "PASSWORD_HASH_INVALID"):
		return wrap(ErrPasswordInvalid, err)
	case tgerr.Is(err, "PHONE_NUMBER_INVALID", "PHONE_NUMBER_UNOCCUPIED"):
		return wrap(ErrPhoneInvalid, err)
	case tgerr.Is(err, "PHONE_NUMBER_BANNED"):
		return wrap(ErrPhoneBanned, err)
	case tgerr.Is(err, "PHONE_CODE_INVALID", "PHONE_CODE_EMPTY"):
		return wrap(ErrInvalidCode, err)
	case tgerr.Is(err, "PHONE_CODE_EXPIRED", "PHONE_CODE_HASH_EMPTY"):
		return wrap(ErrCodeExpired, err)
	case isAuthLost(err):
		// Also matches ErrNotReady so callers only interested in
		// availability need a single check.
		return fmt.Errorf("%w: %w: %w", ErrNotReady, ErrAuthLost, err)
	}
	return err
}
