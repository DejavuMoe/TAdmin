package userbot

import (
	"strings"
	"unicode/utf8"
)

// Outcome classifies the reply of @WebpageBot.
type Outcome string

// Refresh outcomes.
const (
	// OutcomeUpdated means the bot confirmed the preview was refreshed.
	OutcomeUpdated Outcome = "updated"
	// OutcomeNoPreview means the bot reported that no preview is available.
	OutcomeNoPreview Outcome = "no_preview"
	// OutcomeUnknown means the reply could not be classified or never arrived.
	OutcomeUnknown Outcome = "unknown"
)

// Negative markers are checked first so that replies such as
// "Sorry, the preview couldn't be updated" are not taken as success.
var (
	noPreviewMarkers = []string{
		"no preview", "no link preview", "not found", "couldn't", "could not",
		"can't", "cannot", "unable", "invalid", "sorry", "failed", "not updated",
	}
	updatedMarkers = []string{"updated", "refreshed"}
)

// ClassifyReply maps a @WebpageBot reply to an Outcome (case-insensitive).
func ClassifyReply(text string) Outcome {
	t := strings.ToLower(strings.ReplaceAll(text, "’", "'"))
	if strings.TrimSpace(t) == "" {
		return OutcomeUnknown
	}
	for _, m := range noPreviewMarkers {
		if strings.Contains(t, m) {
			return OutcomeNoPreview
		}
	}
	for _, m := range updatedMarkers {
		if strings.Contains(t, m) {
			return OutcomeUpdated
		}
	}
	return OutcomeUnknown
}

// MaskPhone hides the middle digits of a phone number, keeping the country
// prefix hint and the last four digits, e.g. "8613812341234" becomes
// "+86*******1234". Non-digit characters are ignored.
func MaskPhone(phone string) string {
	digits := asciiDigits(phone)
	n := len(digits)
	switch {
	case n == 0:
		return ""
	case n <= 4:
		return "+" + strings.Repeat("*", n)
	case n <= 6:
		return "+" + strings.Repeat("*", n-2) + digits[n-2:]
	default:
		return "+" + digits[:2] + strings.Repeat("*", n-6) + digits[n-4:]
	}
}

// NormalizeCode strips separators (spaces, dashes, dots, ...) from a login
// code typed by a user and converts full-width digits to ASCII.
func NormalizeCode(code string) string {
	var b strings.Builder
	for _, r := range code {
		r = foldDigit(r)
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormalizePhone reduces a phone number to its digits (international format
// without the leading "+" or "00"). It returns ErrPhoneInvalid when the
// result cannot be a valid E.164 number.
func NormalizePhone(phone string) (string, error) {
	s := strings.TrimSpace(phone)
	for _, r := range s {
		r = foldDigit(r)
		if (r < '0' || r > '9') && !strings.ContainsRune("+-() . ", r) {
			return "", ErrPhoneInvalid
		}
	}
	digits := asciiDigits(s)
	if !strings.HasPrefix(s, "+") && strings.HasPrefix(digits, "00") {
		digits = digits[2:]
	}
	if len(digits) < 7 || len(digits) > 15 || digits[0] == '0' {
		return "", ErrPhoneInvalid
	}
	return digits, nil
}

// asciiDigits returns only the digits of s (full-width digits folded).
func asciiDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r = foldDigit(r); r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// foldDigit converts full-width digits (common with CJK input methods) to ASCII.
func foldDigit(r rune) rune {
	if r >= '０' && r <= '９' {
		return '0' + (r - '０')
	}
	return r
}

// truncateRunes returns at most n runes of s.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}
