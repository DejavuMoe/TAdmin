package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestEnvOnlyConfig(t *testing.T) {
	cfg, err := Parse(nil, env(map[string]string{"TADMIN_BOT_TOKEN": "1:abc", "TADMIN_OWNER_ID": "42"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OwnerID != 42 || cfg.BotToken != "1:abc" || cfg.WebEnabled() || cfg.UserbotEnabled() {
		t.Fatalf("unexpected %+v", cfg)
	}
	if cfg.Preview.MinGap.Duration != 45*time.Second {
		t.Fatalf("defaults not applied")
	}
}

func TestSampleParses(t *testing.T) {
	if _, err := Parse([]byte(Sample), env(map[string]string{"TADMIN_BOT_TOKEN": "1:abc"})); err != nil {
		t.Fatal(err)
	}
}

func TestAIBackendConfig(t *testing.T) {
	for _, tc := range []struct {
		name, provider, base, model string
		valid                       bool
	}{
		{"legacy", "", "", "", true},
		{"jev proxy", "jev", "https://proxy.example/typesafe", "jev-1.13.0", true},
		{"laya", "laya", "http://laya:8000", "convaiinnovations/laya-multilingual", true},
		{"laya defaults", "laya", "http://laya:8000", "", true},
		{"no endpoint", "laya", "", "", false},
		{"wrong model", "laya", "http://laya:8000", "jev-latest", false},
		{"unknown provider", "other", "", "", false},
		{"credentials in URL", "jev", "https://u:secret@example.com", "", false},
		{"query in URL", "jev", "https://example.com?key=secret", "", false},
		{"invalid URL", "laya", "file:///tmp/laya", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse(nil, env(map[string]string{
				"TADMIN_BOT_TOKEN": "1:abc", "TADMIN_OWNER_ID": "42",
				"TADMIN_TYPESAFE_PROVIDER": tc.provider, "TADMIN_TYPESAFE_BASE_URL": tc.base,
				"TADMIN_TYPESAFE_MODEL": tc.model, "TADMIN_TYPESAFE_API_KEY": "local-test-key",
			}))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("URL credential leaked")
			}
			if err == nil && (cfg.TypeSafeBaseURL != tc.base || cfg.TypeSafeModel != tc.model || cfg.TypeSafeAPIKey != "local-test-key") {
				t.Fatal("AI settings lost")
			}
			if tc.name == "legacy" && cfg.TypeSafeProvider != "jev" {
				t.Fatal("legacy backend changed")
			}
		})
	}
}

func TestValidation(t *testing.T) {
	_, err := Parse([]byte(`owner_id = 0
[web]
public_url = "http://x.example"
[userbot]
api_id = 5`), env(nil))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"owner_id", "public_url", "api_hash", "TADMIN_BOT_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	if _, err := Parse([]byte("bogus = 1"), env(map[string]string{"TADMIN_BOT_TOKEN": "1:a", "TADMIN_OWNER_ID": "1"})); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("unknown field not rejected: %v", err)
	}
}
