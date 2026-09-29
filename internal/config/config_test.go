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
