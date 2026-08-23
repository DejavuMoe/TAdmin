package invite

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"tadmin/internal/store"
)

const (
	managerInterval       = 2 * time.Second
	actionRetryAge        = 15 * time.Second
	maximumRevokeAttempts = 5
	inviteListLimit       = 20
	externalTimeout       = 10 * time.Second
)

type Options struct {
	OwnerUserID int64
	BotToken    string
}

type Manager struct {
	database *store.Store
	telegram Telegram
	logger   *slog.Logger
	ownerID  int64
	cryptor  *Cryptor
	now      func() time.Time
	wake     chan struct{}
}

func New(database *store.Store, telegram Telegram, logger *slog.Logger, options Options) (*Manager, error) {
	if database == nil || telegram == nil || options.OwnerUserID <= 0 {
		return nil, ErrUnauthorized
	}
	cryptor, err := NewCryptor(options.BotToken)
	if err != nil {
		return nil, err
	}
	return &Manager{
		database: database, telegram: telegram, logger: logger, ownerID: options.OwnerUserID,
		cryptor: cryptor, now: time.Now, wake: make(chan struct{}, 1),
	}, nil
}

func (m *Manager) Wake() {
	if m == nil {
		return
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) Run(ctx context.Context) {
	if m == nil {
		return
	}
	ticker := time.NewTicker(managerInterval)
	defer ticker.Stop()
	m.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reconcile(ctx)
		case <-m.wake:
			m.reconcile(ctx)
		}
	}
}

func (m *Manager) Create(ctx context.Context, operationKey string, chatID, actorUserID int64, rawName, language string) (OperationResult, error) {
	if actorUserID != m.ownerID {
		return OperationResult{}, ErrUnauthorized
	}
	name, err := NormalizeName(rawName)
	if err != nil {
		return OperationResult{}, err
	}
	now := m.currentTime()
	var record store.InviteLink
	var created bool
	for range 4 {
		publicID, err := newPublicID()
		if err != nil {
			return OperationResult{}, err
		}
		record, created, err = m.database.CreateInviteLinkRecord(ctx, store.InviteLink{
			PublicID: publicID, CreateOperationKey: operationKey, ChatID: chatID,
			DisplayName: name, TelegramName: "TAdmin " + publicID, CreatorUserID: actorUserID,
		}, now)
		if err == nil {
			break
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique constraint failed: invite_links.public_id") {
			return OperationResult{}, err
		}
	}
	if err != nil {
		return OperationResult{}, err
	}
	base := OperationResult{PublicID: record.PublicID, Name: record.DisplayName, Status: record.Status}
	if !created {
		base.Code = ResultExisting
		return base, nil
	}

	requestContext, cancel := context.WithTimeout(ctx, externalTimeout)
	err = m.telegram.SendInvitePreflight(requestContext, m.ownerID, preflightMessage(language, record.PublicID, record.DisplayName))
	cancel()
	if err != nil {
		if completeErr := m.database.FailInviteBeforeCreate(ctx, record.PublicID, "preflight_failed", m.currentTime()); completeErr != nil {
			return OperationResult{}, completeErr
		}
		base.Code, base.Status = ResultPreflightFailed, store.InviteFailed
		return base, nil
	}
	claimed, err := m.database.ClaimInviteCreate(ctx, record.PublicID, m.currentTime())
	if err != nil {
		return OperationResult{}, err
	}
	requestContext, cancel = context.WithTimeout(ctx, externalTimeout)
	telegramLink, createErr := m.telegram.CreateJoinRequestInvite(requestContext, claimed.ChatID, claimed.TelegramName)
	cancel()
	if createErr != nil && !validTelegramInviteURL(telegramLink.URL) &&
		(errors.Is(createErr, ErrTelegramPermission) || errors.Is(createErr, ErrTelegramRejected)) {
		resultCode, resultText := ResultCreateRejected, "telegram_rejected"
		if errors.Is(createErr, ErrTelegramPermission) {
			resultCode, resultText = ResultPermissionDenied, "permission_denied"
		}
		if markErr := m.database.MarkInviteCreateFailed(ctx, claimed.PublicID, resultText, m.currentTime()); markErr != nil {
			return OperationResult{}, markErr
		}
		m.logExternalFailure("create invite link", claimed)
		base.Code, base.Status = resultCode, store.InviteFailed
		return base, nil
	}
	if createErr != nil || !telegramLink.CreatesJoinRequest || !validTelegramInviteURL(telegramLink.URL) {
		if validTelegramInviteURL(telegramLink.URL) {
			m.bestEffortRevoke(ctx, claimed.ChatID, telegramLink.URL)
		}
		if markErr := m.database.MarkInviteCreateIndeterminate(ctx, claimed.PublicID, "create_result_unknown", m.currentTime()); markErr != nil {
			return OperationResult{}, markErr
		}
		m.logExternalFailure("create invite link", claimed)
		base.Code, base.Status = ResultCreateIndeterminate, store.InviteIndeterminate
		return base, nil
	}
	ciphertext, nonce, fingerprint, err := m.cryptor.Encrypt(claimed.ChatID, claimed.PublicID, telegramLink.URL)
	if err != nil {
		m.bestEffortRevoke(ctx, claimed.ChatID, telegramLink.URL)
		_ = m.database.MarkInviteCreateIndeterminate(ctx, claimed.PublicID, "encryption_failed", m.currentTime())
		return OperationResult{}, err
	}
	if err := m.database.ActivateInviteLink(ctx, claimed.PublicID, ciphertext, nonce, fingerprint, m.currentTime()); err != nil {
		m.bestEffortRevoke(ctx, claimed.ChatID, telegramLink.URL)
		return OperationResult{}, err
	}
	requestContext, cancel = context.WithTimeout(ctx, externalTimeout)
	deliveryErr := m.telegram.SendInviteSecret(requestContext, m.ownerID,
		secretMessage(language, claimed.PublicID, claimed.DisplayName), telegramLink.URL)
	cancel()
	if deliveryErr == nil {
		if err := m.database.MarkInviteDelivered(ctx, claimed.PublicID, m.currentTime()); err != nil {
			if requested, _, requestErr := m.database.RequestInviteRevocation(ctx, claimed.ChatID, claimed.PublicID,
				"delivery:"+claimed.PublicID, m.ownerID, m.currentTime()); requestErr == nil {
				m.processSpecificRevocation(ctx, requested.PublicID)
			}
			return OperationResult{}, err
		}
		base.Code, base.Status = ResultDelivered, store.InviteActive
		return base, nil
	}
	requested, _, requestErr := m.database.RequestInviteRevocation(ctx, claimed.ChatID, claimed.PublicID,
		"delivery:"+claimed.PublicID, m.ownerID, m.currentTime())
	if requestErr != nil {
		return OperationResult{}, requestErr
	}
	code := m.processSpecificRevocation(ctx, requested.PublicID)
	base.Status = store.InviteRevocationPending
	if code == ResultRevoked {
		base.Code, base.Status = ResultDeliveryFailedRevoked, store.InviteRevoked
	} else {
		base.Code = ResultRevocationPending
	}
	return base, nil
}

func (m *Manager) List(ctx context.Context, chatID, actorUserID int64) ([]store.InviteSummary, error) {
	if actorUserID != m.ownerID {
		return nil, ErrUnauthorized
	}
	chat, err := m.database.GetManagedChat(ctx, chatID)
	if err != nil || !chat.Enabled {
		if err == nil {
			err = store.ErrManagedChatNotFound
		}
		return nil, err
	}
	return m.database.ListInviteLinks(ctx, chatID, inviteListLimit)
}

func (m *Manager) Revoke(ctx context.Context, operationKey string, chatID, actorUserID int64, publicID string) (OperationResult, error) {
	if actorUserID != m.ownerID {
		return OperationResult{}, ErrUnauthorized
	}
	if !validPublicID(publicID) {
		return OperationResult{}, ErrInvalidID
	}
	chat, err := m.database.GetManagedChat(ctx, chatID)
	if err != nil || !chat.Enabled {
		if err == nil {
			err = store.ErrManagedChatNotFound
		}
		return OperationResult{}, err
	}
	record, changed, err := m.database.RequestInviteRevocation(ctx, chatID, publicID, operationKey,
		actorUserID, m.currentTime())
	if errors.Is(err, store.ErrInviteState) {
		return OperationResult{PublicID: publicID, Code: ResultUnavailable}, nil
	}
	if err != nil {
		return OperationResult{}, err
	}
	result := OperationResult{PublicID: record.PublicID, Name: record.DisplayName, Status: record.Status}
	if record.Status == store.InviteRevoked {
		result.Code = ResultAlreadyRevoked
		return result, nil
	}
	if !changed && record.ActionState == store.InviteActionProcessing {
		result.Code = ResultRevocationPending
		return result, nil
	}
	result.Code = m.processSpecificRevocation(ctx, record.PublicID)
	if result.Code == ResultRevoked {
		result.Status = store.InviteRevoked
	} else {
		result.Status = store.InviteRevocationPending
		m.Wake()
	}
	return result, nil
}

func (m *Manager) processSpecificRevocation(ctx context.Context, publicID string) string {
	now := m.currentTime()
	record, err := m.database.ClaimInviteRevocation(ctx, publicID, now.Add(-actionRetryAge), now, maximumRevokeAttempts)
	if err != nil {
		return ResultRevocationPending
	}
	return m.executeRevocation(ctx, record)
}

func (m *Manager) executeRevocation(ctx context.Context, record store.InviteLink) string {
	inviteURL, err := m.cryptor.Decrypt(record.ChatID, record.PublicID,
		record.EncryptedLink, record.LinkNonce, record.LinkFingerprint)
	if err != nil {
		_ = m.database.FailInviteRevocation(ctx, record.ID, "decrypt_failed", m.currentTime())
		m.logExternalFailure("decrypt invite link", record)
		return ResultDecryptFailed
	}
	requestContext, cancel := context.WithTimeout(ctx, externalTimeout)
	err = m.telegram.RevokeInvite(requestContext, record.ChatID, inviteURL)
	cancel()
	if err != nil {
		result, code := "telegram_error", ResultRevocationPending
		if errors.Is(err, ErrTelegramPermission) {
			result, code = "permission_denied", ResultPermissionDenied
		}
		_ = m.database.FailInviteRevocation(ctx, record.ID, result, m.currentTime())
		m.logExternalFailure("revoke invite link", record)
		return code
	}
	if err := m.database.CompleteInviteRevocation(ctx, record.ID, "revoked", m.currentTime()); err != nil {
		m.logStoreFailure("complete invite revocation", record, err)
		return ResultRevocationPending
	}
	return ResultRevoked
}

func (m *Manager) reconcile(ctx context.Context) {
	now := m.currentTime()
	if err := m.database.SettleStaleInviteCreates(ctx, now.Add(-actionRetryAge), now); err != nil {
		m.logStoreFailure("settle invite creates", store.InviteLink{}, err)
		return
	}
	records, err := m.database.ClaimInviteRevocations(ctx, now.Add(-actionRetryAge), now,
		maximumRevokeAttempts, 20)
	if err != nil {
		m.logStoreFailure("claim invite revocations", store.InviteLink{}, err)
		return
	}
	for _, record := range records {
		if ctx.Err() != nil {
			return
		}
		m.executeRevocation(ctx, record)
	}
}

func (m *Manager) bestEffortRevoke(ctx context.Context, chatID int64, inviteURL string) {
	requestContext, cancel := context.WithTimeout(ctx, externalTimeout)
	defer cancel()
	_ = m.telegram.RevokeInvite(requestContext, chatID, inviteURL)
}

func (m *Manager) currentTime() time.Time {
	return m.now().UTC().Truncate(time.Second)
}

func (m *Manager) logExternalFailure(operation string, record store.InviteLink) {
	if m.logger != nil {
		m.logger.Error(operation, "chat_id", record.ChatID, "invite_public_id", record.PublicID,
			"error_kind", "external_operation_failed")
	}
}

func (m *Manager) logStoreFailure(operation string, record store.InviteLink, err error) {
	if m.logger == nil || err == nil {
		return
	}
	attributes := []any{"error", err}
	if record.PublicID != "" {
		attributes = append(attributes, "chat_id", record.ChatID, "invite_public_id", record.PublicID)
	}
	m.logger.Error(operation, attributes...)
}

func NormalizeName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrInvalidName
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", ErrInvalidName
		}
	}
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || utf8.RuneCountInString(value) > 32 {
		return "", ErrInvalidName
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "http") || strings.Contains(lower, "t.me") || strings.Contains(lower, "joinchat") {
		return "", ErrInvalidName
	}
	for _, character := range value {
		if !(unicode.IsLetter(character) || unicode.IsNumber(character) || unicode.IsSpace(character) ||
			character == '-' || character == '_' || character == '.') {
			return "", ErrInvalidName
		}
	}
	return value, nil
}

func newPublicID() (string, error) {
	random := make([]byte, 10)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate invite public identifier: %w", err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(random)), nil
}

func validPublicID(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= '2' && character <= '7')) {
			return false
		}
	}
	return true
}
