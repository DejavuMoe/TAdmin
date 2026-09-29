package tg

import (
	"html"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Esc escapes text for HTML parse mode.
func Esc(s string) string { return html.EscapeString(s) }

// Mention renders an HTML mention of u with a display name capped at 32 runes.
func Mention(u *User) string {
	if u == nil {
		return ""
	}
	return MentionID(u.ID, u.FullName())
}

// MentionID renders an HTML mention for a user id and display name.
func MentionID(id int64, name string) string {
	name = Truncate(strings.TrimSpace(name), 32)
	if name == "" {
		name = strconv.FormatInt(id, 10)
	}
	return `<a href="tg://user?id=` + strconv.FormatInt(id, 10) + `">` + Esc(name) + `</a>`
}

// Truncate shortens s to at most n runes, appending an ellipsis when cut.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// Keyboard builds an inline keyboard from rows.
func Keyboard(rows ...[]Button) *InlineKeyboard {
	out := make([][]Button, 0, len(rows))
	for _, r := range rows {
		if len(r) > 0 {
			out = append(out, r)
		}
	}
	return &InlineKeyboard{InlineKeyboard: out}
}

// Row is a helper for a keyboard row.
func Row(buttons ...Button) []Button { return buttons }

// CB is a callback button.
func CB(text, data string) Button { return Button{Text: text, CallbackData: data} }

// CBStyle is a styled callback button.
func CBStyle(text, data, style string) Button {
	return Button{Text: text, CallbackData: data, Style: style}
}

// URLButton is a URL button.
func URLButton(text, url string) Button { return Button{Text: text, URL: url} }

// CommandArgs splits "/cmd@bot a b" into ("cmd", "bot", "a b").
func CommandArgs(text string) (cmd, target, args string) {
	if !strings.HasPrefix(text, "/") {
		return "", "", ""
	}
	head, rest := text[1:], ""
	if i := strings.IndexAny(head, " \n\t"); i >= 0 {
		head, rest = head[:i], head[i+1:]
	}
	cmd, target, _ = strings.Cut(head, "@")
	return strings.ToLower(cmd), target, strings.TrimSpace(rest)
}
