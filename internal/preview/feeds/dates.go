package feeds

import (
	"strings"
	"time"
)

// isoLayouts cover W3C datetime (sitemaps, Atom, JSON Feed) and common
// sloppy ISO-8601 variants. Fractional seconds are accepted by time.Parse
// after the seconds field even when the layout omits them.
var isoLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04:05Z0700",
	"2006-01-02T15:04Z0700",
	"2006-01-02T15:04:05Z07",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05 Z07:00",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05 MST",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006-01",
	"2006",
}

// rfcLayouts cover RFC 822/1123/850 dates and common variants, applied after
// commas and the leading weekday are removed and well-known zone
// abbreviations are replaced by numeric offsets.
var rfcLayouts = []string{
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 -07:00",
	"2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05",
	"2 Jan 2006 15:04 -0700",
	"2 Jan 2006 15:04 MST",
	"2 Jan 2006 15:04",
	"2 Jan 06 15:04:05 -0700",
	"2 Jan 06 15:04:05 MST",
	"2 Jan 06 15:04 -0700",
	"2 Jan 06 15:04 MST",
	"2 January 2006 15:04:05 -0700",
	"2 January 2006 15:04:05 MST",
	"2 January 2006 15:04 -0700",
	"2 January 2006 15:04:05",
	"2-Jan-06 15:04:05 -0700",
	"2-Jan-06 15:04:05 MST",
	"2-Jan-2006 15:04:05 -0700",
	"2-Jan-2006 15:04:05 MST",
	"2 Jan 2006",
	"2 January 2006",
	"Jan 2 2006 15:04:05 -0700",
	"Jan 2 2006 15:04:05 MST",
	"Jan 2 2006 15:04:05",
	"Jan 2 2006 15:04 -0700",
	"Jan 2 2006",
	"January 2 2006 15:04:05 -0700",
	"January 2 2006",
	"Jan 2 15:04:05 2006",           // ANSI C without weekday
	"Jan 2 15:04:05 MST 2006",       // Unix date without weekday
	"Jan 2 15:04:05 -0700 2006",     // Ruby date without weekday
	"Jan 2 15:04:05 -0700 MST 2006", // date(1) with both
}

var zoneOffsets = map[string]string{
	"UT": "+0000", "UTC": "+0000", "GMT": "+0000", "Z": "+0000", "WET": "+0000",
	"EST": "-0500", "EDT": "-0400", "CST": "-0600", "CDT": "-0500",
	"MST": "-0700", "MDT": "-0600", "PST": "-0800", "PDT": "-0700",
	"AKST": "-0900", "AKDT": "-0800", "HST": "-1000",
	"BST": "+0100", "CET": "+0100", "CEST": "+0200", "MET": "+0100", "MEST": "+0200",
	"EET": "+0200", "EEST": "+0300", "WEST": "+0100", "MSK": "+0300",
	"JST": "+0900", "KST": "+0900", "HKT": "+0800", "SGT": "+0800", "AWST": "+0800",
	"AEST": "+1000", "AEDT": "+1100", "ACST": "+0930", "NZST": "+1200", "NZDT": "+1300",
}

var weekdayPrefixes = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// parseDate parses the date formats found in sitemaps and feeds. It returns
// the zero time when s is empty or unrecognized. Results are in UTC.
func parseDate(s string) time.Time {
	s = cleanText(s)
	if s == "" {
		return time.Time{}
	}
	if s[0] >= '0' && s[0] <= '9' {
		iso := strings.ToUpper(s)
		for _, layout := range isoLayouts {
			if t, err := time.Parse(layout, iso); err == nil {
				return t.UTC()
			}
		}
	}
	s = normalizeRFCDate(s)
	for _, layout := range rfcLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// normalizeRFCDate removes commas, a leading weekday and parenthesized
// comments, and maps well-known zone abbreviations to numeric offsets.
func normalizeRFCDate(s string) string {
	fields := strings.Fields(strings.ReplaceAll(s, ",", " "))
	if len(fields) > 0 && isWeekday(fields[0]) {
		fields = fields[1:]
	}
	out := fields[:0]
	for _, f := range fields {
		if strings.HasPrefix(f, "(") {
			continue
		}
		if off, ok := zoneOffsets[strings.ToUpper(f)]; ok {
			f = off
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

func isWeekday(tok string) bool {
	tok = strings.TrimSuffix(strings.ToLower(tok), ".")
	if len(tok) < 3 || len(tok) > 9 {
		return false
	}
	for _, r := range tok {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	for _, p := range weekdayPrefixes {
		if strings.HasPrefix(tok, p) {
			return true
		}
	}
	return false
}

// firstDate returns the first of values that parses as a date.
func firstDate(values ...string) time.Time {
	for _, v := range values {
		if t := parseDate(v); !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}
