package bot

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tadmin/internal/i18n"
	"tadmin/internal/preview/feeds"
	"tadmin/internal/tg"
)

func t(lang, key string, args ...any) string { return i18n.T(lang, key, args...) }

// session is a pending text input in a private chat.
type session struct {
	kind    string // welcome_text | welcome_buttons | add_source | phone | password
	chatID  int64
	msgID   int // panel message to refresh afterwards
	cands   []feeds.Candidate
	code    string
	expires time.Time
}

type sessions struct {
	mu sync.Mutex
	m  map[int64]*session
}

func newSessions() *sessions { return &sessions{m: map[int64]*session{}} }

func (s *sessions) get(user int64) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	x := s.m[user]
	if x != nil && time.Now().After(x.expires) {
		delete(s.m, user)
		return nil
	}
	return x
}

func (s *sessions) set(user int64, x *session) {
	x.expires = time.Now().Add(15 * time.Minute)
	s.mu.Lock()
	s.m[user] = x
	s.mu.Unlock()
}

func (s *sessions) clear(user int64) {
	s.mu.Lock()
	delete(s.m, user)
	s.mu.Unlock()
}

// screen is a rendered panel page.
type screen struct {
	text string
	kb   *tg.InlineKeyboard
}

// show edits the panel message in place, or sends a new one.
func (b *Bot) show(ctx context.Context, chatID int64, msgID int, s screen) int {
	if msgID != 0 {
		err := b.tg.EditMessageText(ctx, tg.EditMessageTextParams{ChatID: chatID, MessageID: msgID, Text: s.text, ReplyMarkup: s.kb})
		if err == nil {
			return msgID
		}
		// Photos (QR codes) cannot become text; fall through to a new message.
	}
	m, err := b.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: chatID, Text: s.text, ReplyMarkup: s.kb})
	if err != nil {
		b.log.Warn("send panel", "error", err)
		return 0
	}
	return m.MessageID
}

func onOff(lang string, v bool) string {
	if v {
		return t(lang, "on")
	}
	return t(lang, "off")
}

func check(v bool) string {
	if v {
		return "✅"
	}
	return "▫️"
}

// toggle renders "✅ Label" / "▫️ Label".
func toggle(label string, v bool, data string) tg.Button {
	return tg.CB(check(v)+" "+label, data)
}

func back(lang, data string) tg.Button { return tg.CB(t(lang, "btn_back"), data) }

var sparkChars = []rune("▁▂▃▄▅▆▇█")

// sparkline renders values as a unicode bar chart.
func sparkline(vals []int) string {
	mx := 0
	for _, v := range vals {
		mx = max(mx, v)
	}
	var b strings.Builder
	for _, v := range vals {
		if mx == 0 || v == 0 {
			b.WriteRune('▁')
			continue
		}
		idx := (v*(len(sparkChars)-1) + mx - 1) / mx
		b.WriteRune(sparkChars[min(idx, len(sparkChars)-1)])
	}
	return b.String()
}

func median(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func pct(a, b int) string {
	if b == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", float64(a)*100/float64(b))
}

func humanDuration(d time.Duration, lang string) string {
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if lang == "en" {
		switch {
		case days > 0:
			return fmt.Sprintf("%dd %dh", days, hours)
		case hours > 0:
			return fmt.Sprintf("%dh %dm", hours, mins)
		}
		return fmt.Sprintf("%dm", mins)
	}
	switch {
	case days > 0:
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d 小时 %d 分", hours, mins)
	}
	return fmt.Sprintf("%d 分钟", mins)
}

func humanSeconds(sec int, lang string) string {
	if sec%60 == 0 && sec >= 60 {
		return t(lang, "n_minutes", sec/60)
	}
	return t(lang, "n_seconds", sec)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// cycle returns the element after cur in list (wrapping).
func cycle[T comparable](list []T, cur T) T {
	for i, v := range list {
		if v == cur {
			return list[(i+1)%len(list)]
		}
	}
	return list[0]
}

// step moves cur to the neighbour in an ordered list.
func stepIn(list []int, cur, dir int) int {
	idx := 0
	for i, v := range list {
		if v <= cur {
			idx = i
		}
	}
	idx = max(0, min(len(list)-1, idx+dir))
	return list[idx]
}

// parseDuration understands 30s, 10m, 2h, 3d, 1w; bare numbers are minutes.
func parseDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, false
	}
	unit := time.Minute
	switch s[len(s)-1] {
	case 's':
		unit, s = time.Second, s[:len(s)-1]
	case 'm':
		unit, s = time.Minute, s[:len(s)-1]
	case 'h':
		unit, s = time.Hour, s[:len(s)-1]
	case 'd':
		unit, s = 24*time.Hour, s[:len(s)-1]
	case 'w':
		unit, s = 7*24*time.Hour, s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n > 100000 || n > math.MaxInt64/int64(unit) {
		return 0, false
	}
	return time.Duration(n) * unit, true
}
