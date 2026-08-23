package invite

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
)

const (
	encryptionKeyContext  = "TAdmin invite encryption key v1"
	fingerprintKeyContext = "TAdmin invite fingerprint key v1"
)

type Cryptor struct {
	aead           cipher.AEAD
	fingerprintKey [sha256.Size]byte
}

func NewCryptor(botToken string) (*Cryptor, error) {
	botToken = strings.TrimSpace(botToken)
	if botToken == "" {
		return nil, ErrCiphertext
	}
	encryptionKey := deriveKey(botToken, encryptionKeyContext)
	block, err := aes.NewCipher(encryptionKey[:])
	if err != nil {
		return nil, fmt.Errorf("initialize invite encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize invite authenticated encryption: %w", err)
	}
	return &Cryptor{
		aead:           aead,
		fingerprintKey: deriveKey(botToken, fingerprintKeyContext),
	}, nil
}

func (c *Cryptor) Encrypt(chatID int64, publicID, inviteURL string) (ciphertext, nonce, fingerprint []byte, err error) {
	if c == nil || c.aead == nil || chatID == 0 || !validPublicID(publicID) || !validTelegramInviteURL(inviteURL) {
		return nil, nil, nil, ErrInviteURL
	}
	nonce = make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, nil, fmt.Errorf("generate invite encryption nonce: %w", err)
	}
	aad := inviteAAD(chatID, publicID)
	ciphertext = c.aead.Seal(nil, nonce, []byte(inviteURL), aad)
	fingerprint = c.fingerprint(aad, inviteURL)
	return ciphertext, nonce, fingerprint, nil
}

func (c *Cryptor) Decrypt(chatID int64, publicID string, ciphertext, nonce, fingerprint []byte) (string, error) {
	if c == nil || c.aead == nil || chatID == 0 || !validPublicID(publicID) ||
		len(nonce) != c.aead.NonceSize() || len(ciphertext) == 0 || len(fingerprint) != sha256.Size {
		return "", ErrCiphertext
	}
	aad := inviteAAD(chatID, publicID)
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", ErrCiphertext
	}
	inviteURL := string(plaintext)
	expected := c.fingerprint(aad, inviteURL)
	if subtle.ConstantTimeCompare(fingerprint, expected) != 1 || !validTelegramInviteURL(inviteURL) {
		return "", ErrCiphertext
	}
	return inviteURL, nil
}

func deriveKey(botToken, contextLabel string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, []byte(botToken))
	_, _ = mac.Write([]byte(contextLabel))
	var key [sha256.Size]byte
	copy(key[:], mac.Sum(nil))
	return key
}

func (c *Cryptor) fingerprint(aad []byte, inviteURL string) []byte {
	mac := hmac.New(sha256.New, c.fingerprintKey[:])
	_, _ = mac.Write(aad)
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(inviteURL))
	return mac.Sum(nil)
}

func inviteAAD(chatID int64, publicID string) []byte {
	result := make([]byte, 8+len(publicID)+1)
	binary.BigEndian.PutUint64(result[:8], uint64(chatID))
	copy(result[8:], publicID)
	result[len(result)-1] = 1
	return result
}

func validTelegramInviteURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "t.me") {
		return false
	}
	path := strings.Trim(parsed.EscapedPath(), "/")
	if strings.HasPrefix(path, "+") {
		return len(path) > 1 && !strings.Contains(path[1:], "/")
	}
	const legacyPrefix = "joinchat/"
	return strings.HasPrefix(strings.ToLower(path), legacyPrefix) && len(path) > len(legacyPrefix) &&
		!strings.Contains(path[len(legacyPrefix):], "/")
}
