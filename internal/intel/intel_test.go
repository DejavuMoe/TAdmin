package intel

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		risk           float64
		elevated, high float64
		want           Level
	}{
		{0, DefaultElevated, DefaultHigh, LevelNone},
		{0.59, DefaultElevated, DefaultHigh, LevelNone},
		{0.6, DefaultElevated, DefaultHigh, LevelElevated},
		{0.84, DefaultElevated, DefaultHigh, LevelElevated},
		{0.85, DefaultElevated, DefaultHigh, LevelHigh},
		{1, DefaultElevated, DefaultHigh, LevelHigh},
		{0.7, 0, 0, LevelElevated},         // zero thresholds use defaults
		{0.9, math.NaN(), -1, LevelHigh},   // invalid thresholds use defaults
		{0.5, 0.4, 0.5, LevelHigh},         // custom thresholds
		{0.45, 0.4, 0.5, LevelElevated},    // custom thresholds
		{0.75, 0.9, 0.7, LevelHigh},        // elevated capped at high
		{math.NaN(), 0.6, 0.85, LevelNone}, // NaN risk never escalates
	}
	for _, c := range cases {
		if got := Classify(Assessment{Risk: c.risk}, c.elevated, c.high); got != c.want {
			t.Errorf("Classify(risk=%v, %v, %v) = %v, want %v", c.risk, c.elevated, c.high, got, c.want)
		}
	}
}

func TestLevelString(t *testing.T) {
	if LevelNone.String() != "none" || LevelElevated.String() != "elevated" || LevelHigh.String() != "high" {
		t.Fatal("unexpected level names")
	}
}

func TestOptionsStringRedactsKey(t *testing.T) {
	o := Options{APIKey: "sk-secret", Model: "jev-latest"}
	for _, s := range []string{fmt.Sprint(o), fmt.Sprintf("%+v", o), fmt.Sprintf("%#v", o)} {
		if strings.Contains(s, "sk-secret") {
			t.Fatalf("api key leaked: %s", s)
		}
	}
}

func TestBuildStateTruncatesAndOmitsIdentifiers(t *testing.T) {
	photo := true
	p := Profile{
		UserID:          123456789,
		FirstName:       strings.Repeat("名", 100),
		Username:        "@spam_bot",
		Bio:             strings.Repeat("a", 500) + "\x00",
		ChatTitle:       "Go 语言交流",
		ChatDescription: strings.Repeat("d", 400),
		HasPhoto:        &photo,
	}
	s := buildState(p)
	if n := utf8.RuneCountInString(s.JoiningAccount.FirstName); n != maxNameRunes {
		t.Errorf("first name runes = %d", n)
	}
	if n := utf8.RuneCountInString(s.JoiningAccount.Bio); n != maxBioRunes {
		t.Errorf("bio runes = %d", n)
	}
	if n := utf8.RuneCountInString(s.Group.Description); n != maxChatDescRunes {
		t.Errorf("description runes = %d", n)
	}
	if s.JoiningAccount.Username != "spam_bot" {
		t.Errorf("username = %q", s.JoiningAccount.Username)
	}
	if !strings.HasSuffix(s.JoiningAccount.Bio, truncationIndicator) {
		t.Error("truncated bio lacks indicator")
	}
	if strings.ContainsRune(s.JoiningAccount.Bio, 0) {
		t.Error("control character kept")
	}
	if got := clean("line1\nline2\t", 64); got != "line1\nline2" {
		t.Errorf("clean = %q", got)
	}
	if got := clean("\xff\xfeok", 64); !utf8.ValidString(got) {
		t.Errorf("clean produced invalid utf-8: %q", got)
	}
}

func TestQuestionsCoverRiskSignals(t *testing.T) {
	for _, id := range RiskSignals() {
		if _, ok := questions[id]; !ok {
			t.Errorf("risk signal %q has no question", id)
		}
	}
	for id, q := range questions {
		if q.Type != "noul" || q.Instructions.Question == "" || q.Criteria.True.What == "" || q.Criteria.False.What == "" {
			t.Errorf("question %q incomplete", id)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			out.Set(kv[i], kv[i+1])
		}
		return out
	}
	cases := []struct {
		h    http.Header
		want time.Duration
	}{
		{h(), 0},
		{h("Retry-After", "3"), 3 * time.Second},
		{h("Retry-After", "1.5"), 1500 * time.Millisecond},
		{h("retry-after-ms", "250", "Retry-After", "9"), 250 * time.Millisecond},
		{h("Retry-After", now.Add(10*time.Second).Format(http.TimeFormat)), 10 * time.Second},
		{h("Retry-After", now.Add(-time.Minute).Format(http.TimeFormat)), 0},
		{h("Retry-After", "garbage"), 0},
		{h("Retry-After", "-5"), 0},
		{h("Retry-After", "999999"), maxRetryAfter},
	}
	for i, c := range cases {
		if got := parseRetryAfter(c.h, now); got != c.want {
			t.Errorf("case %d: got %v, want %v", i, got, c.want)
		}
	}
}
