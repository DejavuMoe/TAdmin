package config

import (
	"strings"
	"testing"
)

const validConfig = `
owner_user_id = 123456789
database_path = "data/tadmin.db"
public_base_url = "https://verify.example.com"
management_chat_id = -1001234567890
`

func TestParseAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(validConfig), "123456:secret")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.ListenAddress != "127.0.0.1:8080" {
		t.Errorf("ListenAddress = %q", cfg.ListenAddress)
	}
	if cfg.DefaultLanguage != "zh-CN" {
		t.Errorf("DefaultLanguage = %q", cfg.DefaultLanguage)
	}
	if cfg.BotToken != "123456:secret" {
		t.Errorf("BotToken was not loaded from the environment value")
	}
}

func TestParseRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		contents  string
		token     string
		wantError string
	}{
		{
			name:      "missing owner",
			contents:  `database_path = "data.db"`,
			token:     "token",
			wantError: "owner_user_id",
		},
		{
			name:      "missing database",
			contents:  `owner_user_id = 1`,
			token:     "token",
			wantError: "database_path",
		},
		{
			name: "public URL is not HTTPS",
			contents: `owner_user_id = 1
database_path = "data.db"
public_base_url = "http://example.com"`,
			token:     "token",
			wantError: "HTTPS",
		},
		{
			name: "listen address is public",
			contents: `owner_user_id = 1
database_path = "data.db"
listen_address = "0.0.0.0:8080"`,
			token:     "token",
			wantError: "loopback",
		},
		{
			name: "unsupported language",
			contents: `owner_user_id = 1
database_path = "data.db"
default_language = "fr"`,
			token:     "token",
			wantError: "default_language",
		},
		{
			name:      "missing environment token",
			contents:  validConfig,
			wantError: tokenEnvironmentVariable,
		},
		{
			name: "unknown field",
			contents: `owner_user_id = 1
database_path = "data.db"
unexpected = true`,
			token:     "token",
			wantError: "unexpected",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.contents), test.token)
			if err == nil {
				t.Fatal("Parse() error = nil")
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Parse() error = %q, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestParseAllowsEmptyPublicBaseURLForOfflineDevelopment(t *testing.T) {
	cfg, err := Parse([]byte(`owner_user_id = 1
database_path = "data.db"
public_base_url = ""
default_language = "en"`), "token")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.PublicBaseURL != "" {
		t.Errorf("PublicBaseURL = %q", cfg.PublicBaseURL)
	}
}

func TestPublicURLRejectsUnsupportedSubpath(t *testing.T) {
	_, err := Parse([]byte(strings.Replace(validConfig, "https://verify.example.com", "https://verify.example.com/tadmin", 1)), "synthetic-token")
	if err == nil {
		t.Fatal("subpath would break absolute Mini App routes")
	}
}
