package invite

import (
	"bytes"
	"errors"
	"testing"
)

func TestInviteCryptorRoundTripAndBinding(t *testing.T) {
	cryptor, err := NewCryptor("123456:test-token")
	if err != nil {
		t.Fatal(err)
	}
	const (
		chatID    = int64(-1001)
		publicID  = "abcdefghijklmnop"
		inviteURL = "https://t.me/+private-invite-value"
	)
	ciphertext, nonce, fingerprint, err := cryptor.Encrypt(chatID, publicID, inviteURL)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("private-invite-value")) || len(nonce) != 12 || len(fingerprint) != 32 {
		t.Fatalf("invalid encrypted record: ciphertext=%x nonce=%d fingerprint=%d", ciphertext, len(nonce), len(fingerprint))
	}
	decrypted, err := cryptor.Decrypt(chatID, publicID, ciphertext, nonce, fingerprint)
	if err != nil || decrypted != inviteURL {
		t.Fatalf("decrypt = %q, %v", decrypted, err)
	}
	for name, mutate := range map[string]func() (int64, string, []byte, []byte, []byte){
		"chat": func() (int64, string, []byte, []byte, []byte) {
			return -1002, publicID, ciphertext, nonce, fingerprint
		},
		"public id": func() (int64, string, []byte, []byte, []byte) {
			return chatID, "ponmlkjihgfedcba", ciphertext, nonce, fingerprint
		},
		"ciphertext": func() (int64, string, []byte, []byte, []byte) {
			value := append([]byte(nil), ciphertext...)
			value[0] ^= 1
			return chatID, publicID, value, nonce, fingerprint
		},
		"nonce": func() (int64, string, []byte, []byte, []byte) {
			value := append([]byte(nil), nonce...)
			value[0] ^= 1
			return chatID, publicID, ciphertext, value, fingerprint
		},
		"fingerprint": func() (int64, string, []byte, []byte, []byte) {
			value := append([]byte(nil), fingerprint...)
			value[0] ^= 1
			return chatID, publicID, ciphertext, nonce, value
		},
	} {
		t.Run(name, func(t *testing.T) {
			boundChat, boundID, boundCiphertext, boundNonce, boundFingerprint := mutate()
			if _, err := cryptor.Decrypt(boundChat, boundID, boundCiphertext, boundNonce, boundFingerprint); !errors.Is(err, ErrCiphertext) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestInviteCryptorRejectsWrongTokenAndInvalidURL(t *testing.T) {
	first, _ := NewCryptor("123:first-token")
	second, _ := NewCryptor("123:second-token")
	ciphertext, nonce, fingerprint, err := first.Encrypt(-1001, "abcdefghijklmnop", "https://t.me/joinchat/private-value")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Decrypt(-1001, "abcdefghijklmnop", ciphertext, nonce, fingerprint); !errors.Is(err, ErrCiphertext) {
		t.Fatalf("wrong token error = %v", err)
	}
	for _, value := range []string{
		"http://t.me/+secret", "https://example.com/+secret", "https://t.me/channel", "https://t.me/+secret?leak=1",
		"https://t.me/+", "https://t.me/joinchat/", "https://t.me/+secret/extra", "https://t.me/joinchat/secret/extra",
	} {
		if _, _, _, err := first.Encrypt(-1001, "abcdefghijklmnop", value); !errors.Is(err, ErrInviteURL) {
			t.Fatalf("Encrypt(%q) error = %v", value, err)
		}
	}
}

func TestNormalizeInviteName(t *testing.T) {
	for _, valid := range []string{"Friends", "Release 2026", "中文 邀请", "ops_team-1.0"} {
		if _, err := NormalizeName(valid); err != nil {
			t.Errorf("NormalizeName(%q): %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "has\nnewline", "https://example.com", "t.me", "bad/name", string(make([]byte, 40))} {
		if _, err := NormalizeName(invalid); !errors.Is(err, ErrInvalidName) {
			t.Errorf("NormalizeName(%q) error = %v", invalid, err)
		}
	}
}
