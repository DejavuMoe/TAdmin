// Package i18n holds TAdmin's user-facing strings in Simplified Chinese and
// English. Strings use HTML parse mode; arguments must already be escaped.
package i18n

import (
	"fmt"
	"strings"
)

// Languages.
const (
	ZH = "zh-CN"
	EN = "en"
)

type pair struct{ zh, en string }

var catalog = map[string]pair{}

func add(entries map[string]pair) {
	for k, v := range entries {
		if _, dup := catalog[k]; dup {
			panic("i18n: duplicate key " + k)
		}
		catalog[k] = v
	}
}

// T returns the string for key in lang, formatted with args.
func T(lang, key string, args ...any) string {
	p, ok := catalog[key]
	if !ok {
		return key
	}
	s := p.zh
	if lang == EN && p.en != "" {
		s = p.en
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// Has reports whether key exists.
func Has(key string) bool { _, ok := catalog[key]; return ok }

// Keys returns all keys (tests).
func Keys() []string {
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	return out
}

// Pick returns zh or en depending on lang.
func Pick(lang, zh, en string) string {
	if lang == EN {
		return en
	}
	return zh
}

// Normalize maps a Telegram language code to a supported language.
func Normalize(code, fallback string) string {
	c := strings.ToLower(code)
	switch {
	case c == "":
		return fallback
	case strings.HasPrefix(c, "zh"):
		return ZH
	default:
		return EN
	}
}
