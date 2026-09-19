package verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultInitDataMaxAge = 15 * time.Minute
	DefaultFutureSkew     = 30 * time.Second
	maxInitDataBytes      = 16 << 10
)

var (
	ErrInitDataInvalid = errors.New("invalid Telegram Mini App authorization")
	ErrInitDataExpired = errors.New("Telegram Mini App authorization expired")
	ErrInitDataFuture  = errors.New("Telegram Mini App authorization is from the future")
)

type InitData struct {
	UserID       int64
	AuthDate     time.Time
	QueryID      string
	GuardQueryID string
	ChatID       int64
	StartParam   string
}

type initDataUser struct {
	ID int64 `json:"id"`
}

// ValidateInitData implements Telegram's bot-token HMAC validation algorithm:
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func ValidateInitData(raw, botToken string, now time.Time, maxAge, futureSkew time.Duration) (InitData, error) {
	if raw == "" || len(raw) > maxInitDataBytes || strings.TrimSpace(botToken) == "" || maxAge <= 0 || futureSkew < 0 {
		return InitData{}, ErrInitDataInvalid
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return InitData{}, ErrInitDataInvalid
	}
	for _, entries := range values {
		if len(entries) != 1 {
			return InitData{}, ErrInitDataInvalid
		}
	}

	providedHash, err := hex.DecodeString(values.Get("hash"))
	if err != nil || len(providedHash) != sha256.Size {
		return InitData{}, ErrInitDataInvalid
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		// Telegram defines the bot-token data-check-string as all received
		// fields except the hash being verified. This includes the optional
		// third-party signature field when Telegram supplies it.
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values.Get(key))
	}
	dataCheckString := strings.Join(lines, "\n")

	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMAC.Write([]byte(botToken))
	checkMAC := hmac.New(sha256.New, secretMAC.Sum(nil))
	_, _ = checkMAC.Write([]byte(dataCheckString))
	if subtle.ConstantTimeCompare(providedHash, checkMAC.Sum(nil)) != 1 {
		return InitData{}, ErrInitDataInvalid
	}

	authUnix, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || authUnix <= 0 {
		return InitData{}, ErrInitDataInvalid
	}
	authDate := time.Unix(authUnix, 0).UTC()
	if authDate.After(now.Add(futureSkew)) {
		return InitData{}, ErrInitDataFuture
	}
	if now.Sub(authDate) > maxAge {
		return InitData{}, ErrInitDataExpired
	}

	var user initDataUser
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil || user.ID <= 0 {
		return InitData{}, ErrInitDataInvalid
	}
	var chat struct {
		ID int64 `json:"id"`
	}
	if rawChat := values.Get("chat"); rawChat != "" {
		if json.Unmarshal([]byte(rawChat), &chat) != nil {
			return InitData{}, ErrInitDataInvalid
		}
	}
	return InitData{
		UserID:       user.ID,
		AuthDate:     authDate,
		QueryID:      values.Get("query_id"),
		GuardQueryID: values.Get("chat_join_request_query_id"), ChatID: chat.ID,
		StartParam: values.Get("start_param"),
	}, nil
}
