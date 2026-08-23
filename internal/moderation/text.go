package moderation

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/go-telegram/bot/models"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var (
	plainURLPattern                = regexp.MustCompile(`(?i)(?:https?://|www\.|t\.me/|telegram\.me/)[^\s<>{}\[\]"']+`)
	telegramObfuscatedPattern      = regexp.MustCompile(`(?i)\b(t|telegram)(?:\s*\[\.\]\s*|\s*\(dot\)\s*|\s+dot\s+|\s+\.\s*|\s*\.\s+)me\s*(?:/|\\)`)
	genericObfuscatedDomainPattern = regexp.MustCompile(`(?i)\b([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\s*(?:\[\.\]|\(dot\)|dot)\s*([a-z]{2,63})\b`)
	foldCase                       = cases.Fold()
)

type textAnalysis struct {
	Normalized      string
	Domains         []string
	HasExternalLink bool
	HasTelegramLink bool
	HasObfuscation  bool
	MentionCount    int
}

func analyzeText(text string, entities []models.MessageEntity) textAnalysis {
	normalized, invisible := normalizeText(text)
	domainSet := make(map[string]struct{})
	mentions := make(map[string]struct{})
	analysis := textAnalysis{Normalized: normalized, HasObfuscation: invisible}

	for _, entity := range entities {
		fragment, ok := sliceUTF16(text, entity.Offset, entity.Length)
		if !ok {
			analysis.HasObfuscation = true
			continue
		}
		switch entity.Type {
		case models.MessageEntityTypeMention:
			mentions[foldCase.String(fragment)] = struct{}{}
		case models.MessageEntityTypeTextMention:
			if entity.User != nil {
				mentions[fmt.Sprintf("id:%d", entity.User.ID)] = struct{}{}
			}
		case models.MessageEntityTypeURL:
			addURLDomain(fragment, domainSet, &analysis)
		case models.MessageEntityTypeTextLink:
			actual := addURLDomain(entity.URL, domainSet, &analysis)
			displayed := domainFromURL(fragment)
			if displayed != "" && actual != "" && displayed != actual {
				analysis.HasObfuscation = true
			}
		}
	}

	for _, raw := range plainURLPattern.FindAllString(normalized, -1) {
		addURLDomain(raw, domainSet, &analysis)
	}
	if telegramObfuscatedPattern.MatchString(normalized) {
		analysis.HasObfuscation = true
		domainSet["t.me"] = struct{}{}
		analysis.HasTelegramLink = true
	}
	for _, match := range genericObfuscatedDomainPattern.FindAllStringSubmatch(normalized, -1) {
		if len(match) != 3 {
			continue
		}
		host := strings.ToLower(match[1] + "." + match[2])
		if validDomain(host) {
			analysis.HasObfuscation = true
			domainSet[host] = struct{}{}
			if isTelegramDomain(host) {
				analysis.HasTelegramLink = true
			} else {
				analysis.HasExternalLink = true
			}
		}
	}

	analysis.MentionCount = len(mentions)
	analysis.Domains = make([]string, 0, len(domainSet))
	for domain := range domainSet {
		analysis.Domains = append(analysis.Domains, domain)
	}
	sort.Strings(analysis.Domains)
	return analysis
}

func normalizeText(value string) (string, bool) {
	value = norm.NFKC.String(value)
	var builder strings.Builder
	builder.Grow(len(value))
	invisible := false
	for _, character := range value {
		if isInvisibleControl(character) {
			invisible = true
			continue
		}
		if unicode.IsSpace(character) || unicode.In(character, unicode.Zl, unicode.Zp) {
			builder.WriteByte(' ')
			continue
		}
		builder.WriteRune(character)
	}
	return strings.Join(strings.Fields(foldCase.String(builder.String())), " "), invisible
}

func isInvisibleControl(character rune) bool {
	switch character {
	case '\u061c', '\u200b', '\u200c', '\u200d', '\u200e', '\u200f', '\u202a', '\u202b',
		'\u202c', '\u202d', '\u202e', '\u2060', '\u2061', '\u2062', '\u2063', '\u2064',
		'\u2066', '\u2067', '\u2068', '\u2069', '\ufeff':
		return true
	default:
		return false
	}
}

func sliceUTF16(value string, offset, length int) (string, bool) {
	if offset < 0 || length < 0 {
		return "", false
	}
	units := utf16.Encode([]rune(value))
	end := offset + length
	if end < offset || offset > len(units) || end > len(units) {
		return "", false
	}
	return string(utf16.Decode(units[offset:end])), true
}

func addURLDomain(raw string, domains map[string]struct{}, analysis *textAnalysis) string {
	host := domainFromURL(raw)
	if host == "" {
		return ""
	}
	domains[host] = struct{}{}
	if isTelegramDomain(host) {
		analysis.HasTelegramLink = true
	} else {
		analysis.HasExternalLink = true
	}
	return host
}

func domainFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, ".,!?:;)]}，。！？；：")
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n\t") {
		return ""
	}
	lower := strings.ToLower(raw)
	if !strings.Contains(lower, "://") {
		raw = "https://" + strings.TrimPrefix(raw, "//")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if !validDomain(host) {
		return ""
	}
	return host
}

func validDomain(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /\\@") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character > 127) {
				return false
			}
		}
	}
	return true
}

func isTelegramDomain(host string) bool {
	return host == "t.me" || host == "telegram.me" || strings.HasSuffix(host, ".t.me") || strings.HasSuffix(host, ".telegram.me")
}
