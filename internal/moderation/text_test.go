package moderation

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/go-telegram/bot/models"
)

func TestSliceUTF16AndEntityURLAfterNonBMPCharacter(t *testing.T) {
	text := "😀 visit HTTPS://Example.COM:443/path?secret=token"
	urlText := "HTTPS://Example.COM:443/path?secret=token"
	offset := len(utf16.Encode([]rune("😀 visit ")))
	analysis := analyzeText(text, []models.MessageEntity{{
		Type: models.MessageEntityTypeURL, Offset: offset,
		Length: len(utf16.Encode([]rune(urlText))),
	}})
	if len(analysis.Domains) != 1 || analysis.Domains[0] != "example.com" || !analysis.HasExternalLink {
		t.Fatalf("analysis = %+v", analysis)
	}
	if strings.Contains(strings.Join(analysis.Domains, ""), "secret") {
		t.Fatalf("domain output leaked URL path: %+v", analysis.Domains)
	}
	fragment, ok := sliceUTF16(text, 0, 2)
	if !ok || fragment != "😀" {
		t.Fatalf("sliceUTF16() = %q, %t", fragment, ok)
	}
}

func TestTextLinkMismatchTelegramAndPlainURLExtraction(t *testing.T) {
	text := "good.example and t.me/other_channel"
	displayLength := len(utf16.Encode([]rune("good.example")))
	analysis := analyzeText(text, []models.MessageEntity{{
		Type: models.MessageEntityTypeTextLink, Offset: 0, Length: displayLength,
		URL: "https://evil.example/path/full-secret",
	}})
	if !analysis.HasObfuscation || !analysis.HasExternalLink || !analysis.HasTelegramLink {
		t.Fatalf("analysis = %+v", analysis)
	}
	if got := strings.Join(analysis.Domains, ","); got != "evil.example,t.me" {
		t.Fatalf("domains = %q", got)
	}
}

func TestNormalizationFoldsWidthCaseWhitespaceAndInvisibleControls(t *testing.T) {
	normalized, invisible := normalizeText("  ＴＥＳＴ\u200b\t Foo\u202e  BAR  ")
	if normalized != "test foo bar" || !invisible {
		t.Fatalf("normalizeText() = %q, %t", normalized, invisible)
	}
	analysis := analyzeText("Ｔ\u200b[.]ＭＥ/Channel", nil)
	if !analysis.HasObfuscation || !analysis.HasTelegramLink || len(analysis.Domains) != 1 || analysis.Domains[0] != "t.me" {
		t.Fatalf("obfuscated Telegram analysis = %+v", analysis)
	}
}

func TestInvalidUTF16EntityIsRejectedWithoutPanicking(t *testing.T) {
	analysis := analyzeText("😀 link", []models.MessageEntity{{
		Type: models.MessageEntityTypeURL, Offset: 1, Length: 50,
	}})
	if !analysis.HasObfuscation || len(analysis.Domains) != 0 {
		t.Fatalf("analysis = %+v", analysis)
	}
}
