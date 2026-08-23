package verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const syntheticBotToken = "123456789:synthetic-token-for-tests-only"

func TestValidateInitData(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	values := url.Values{
		"auth_date":   {strconv.FormatInt(now.Unix(), 10)},
		"query_id":    {"query-123"},
		"signature":   {"synthetic-third-party-signature"},
		"start_param": {"session-token"},
		"user":        {`{"id":424242,"first_name":"Test"}`},
	}
	raw := signTestInitData(t, values)

	got, err := ValidateInitData(raw, syntheticBotToken, now, DefaultInitDataMaxAge, DefaultFutureSkew)
	if err != nil {
		t.Fatalf("ValidateInitData() error = %v", err)
	}
	if got.UserID != 424242 || got.QueryID != "query-123" || got.StartParam != "session-token" || !got.AuthDate.Equal(now) {
		t.Fatalf("ValidateInitData() = %+v", got)
	}
}

func TestValidateInitDataRejectsBadSignature(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	values := url.Values{
		"auth_date": {strconv.FormatInt(now.Unix(), 10)},
		"user":      {`{"id":424242}`},
	}
	raw := signTestInitData(t, values)
	parsed, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	hash := []byte(parsed.Get("hash"))
	if hash[0] == '0' {
		hash[0] = '1'
	} else {
		hash[0] = '0'
	}
	parsed.Set("hash", string(hash))

	_, err = ValidateInitData(parsed.Encode(), syntheticBotToken, now, DefaultInitDataMaxAge, DefaultFutureSkew)
	if !errors.Is(err, ErrInitDataInvalid) {
		t.Fatalf("ValidateInitData() error = %v, want ErrInitDataInvalid", err)
	}
}

func TestValidateInitDataRejectsExpiredAndFutureAuthDate(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		authDate time.Time
		want     error
	}{
		{name: "expired", authDate: now.Add(-DefaultInitDataMaxAge - time.Second), want: ErrInitDataExpired},
		{name: "future", authDate: now.Add(DefaultFutureSkew + time.Second), want: ErrInitDataFuture},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := url.Values{
				"auth_date": {strconv.FormatInt(test.authDate.Unix(), 10)},
				"user":      {`{"id":424242}`},
			}
			_, err := ValidateInitData(signTestInitData(t, values), syntheticBotToken, now, DefaultInitDataMaxAge, DefaultFutureSkew)
			if !errors.Is(err, test.want) {
				t.Fatalf("ValidateInitData() error = %v, want %v", err, test.want)
			}
		})
	}
}

func signTestInitData(t *testing.T, values url.Values) string {
	t.Helper()
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values.Get(key))
	}
	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMAC.Write([]byte(syntheticBotToken))
	checkMAC := hmac.New(sha256.New, secretMAC.Sum(nil))
	_, _ = checkMAC.Write([]byte(strings.Join(lines, "\n")))
	hash := hex.EncodeToString(checkMAC.Sum(nil))
	copyValues := make(url.Values, len(values)+1)
	for key, entries := range values {
		copyValues[key] = append([]string(nil), entries...)
	}
	copyValues.Set("hash", hash)
	return copyValues.Encode()
}
