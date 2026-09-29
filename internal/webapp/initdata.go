package webapp

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

// ErrInitData is returned for any invalid Mini App authorization.
var ErrInitData = errors.New("invalid Telegram Mini App init data")

// InitData is the verified subset of Telegram.WebApp.initData.
type InitData struct {
	UserID       int64
	AuthDate     time.Time
	GuardQueryID string
}

// ValidateInitData verifies Telegram's HMAC over the init data string
// (https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app)
// and checks its age.
func ValidateInitData(raw, botToken string, now time.Time, maxAge time.Duration) (InitData, error) {
	if raw == "" || len(raw) > 16<<10 || botToken == "" {
		return InitData{}, ErrInitData
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return InitData{}, ErrInitData
	}
	for _, v := range values {
		if len(v) != 1 {
			return InitData{}, ErrInitData
		}
	}
	hash, err := hex.DecodeString(values.Get("hash"))
	if err != nil || len(hash) != sha256.Size {
		return InitData{}, ErrInitData
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + values.Get(k)
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	if subtle.ConstantTimeCompare(hash, mac.Sum(nil)) != 1 {
		return InitData{}, ErrInitData
	}
	authUnix, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || authUnix <= 0 {
		return InitData{}, ErrInitData
	}
	auth := time.Unix(authUnix, 0)
	if auth.After(now.Add(time.Minute)) || now.Sub(auth) > maxAge {
		return InitData{}, ErrInitData
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil || user.ID <= 0 {
		return InitData{}, ErrInitData
	}
	return InitData{UserID: user.ID, AuthDate: auth, GuardQueryID: values.Get("chat_join_request_query_id")}, nil
}

// SignInitData builds a valid init data string (tests and local demos).
func SignInitData(botToken string, userID int64, auth time.Time, extra map[string]string) string {
	values := url.Values{}
	values.Set("auth_date", strconv.FormatInt(auth.Unix(), 10))
	values.Set("user", `{"id":`+strconv.FormatInt(userID, 10)+`,"first_name":"T"}`)
	for k, v := range extra {
		values.Set(k, v)
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + values.Get(k)
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	values.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return values.Encode()
}
