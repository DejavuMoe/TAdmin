package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dynamic lists the concrete keys behind prefix+variable lookups.
var dynamic = map[string][]string{
	"ai_":               {"off", "hint", "review"},
	"ai_desc_":          {"off", "hint", "review"},
	"delivery_":         {"overlay", "public", "private"},
	"gstate_":           {"new", "challenge", "review", "resolving"},
	"input_":            {"welcome_text", "welcome_buttons"},
	"kind_":             {"image", "math", "button"},
	"kind_src_":         {"sitemap", "sitemapindex", "rss", "atom", "jsonfeed", "html"},
	"lang_":             {"zh-CN", "en"},
	"lbl_onfail_":       {"kick", "review"},
	"pv_result_":        {"updated", "no_preview", "unknown"},
	"source_":           {"join", "request", "guard"},
	"status_":           {"active", "paused", "pending", "left"},
	"ub_code_sent_":     {"app", "sms", "other"},
	"ub_state_":         {"offline", "connecting", "logged_out", "ready", "disabled"},
	"unpin_":            {"off", "unpin", "keep_latest"},
	"unpin_desc_":       {"off", "unpin", "keep_latest"},
	"verify_desc_":      {"overlay", "public", "private"},
	"verify_kind_desc_": {"image", "math", "button"},
	"reason_":           {"help", "failed", "risk", "unreachable", "undeliverable"},
	"result_":           {"pass", "approve", "fail", "timeout", "reject", "ban", "left", "cas", "cancel"},
}

var (
	staticRe  = regexp.MustCompile(`\b(?:i18n\.T|t)\([a-zA-Z.]+, "([a-z0-9_]+)"[,)]`)
	dynamicRe = regexp.MustCompile(`"([a-z_]+_)"\s*\+`)
)

func TestAllReferencedKeysExist(t *testing.T) {
	root := filepath.Join("..")
	seen := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(data)
		for _, m := range staticRe.FindAllStringSubmatch(src, -1) {
			seen++
			if !Has(m[1]) {
				t.Errorf("%s: missing key %q", path, m[1])
			}
		}
		for _, m := range dynamicRe.FindAllStringSubmatch(src, -1) {
			vals, ok := dynamic[m[1]]
			if !ok {
				continue
			}
			for _, v := range vals {
				if !Has(m[1] + v) {
					t.Errorf("%s: missing dynamic key %q", path, m[1]+v)
				}
			}
		}
		// Problem codes rendered with a "_short" suffix.
		for _, m := range regexp.MustCompile(`"(chk_[a-z_]+)"`).FindAllStringSubmatch(src, -1) {
			if !strings.HasSuffix(m[1], "_short") && m[1] != "chk_title" && !Has(m[1]+"_short") {
				t.Errorf("%s: missing %q", path, m[1]+"_short")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 100 {
		t.Fatalf("scanner found only %d references; regexp broken?", seen)
	}
}

func TestTranslationsComplete(t *testing.T) {
	verbs := regexp.MustCompile(`%(\[\d\])?[dsvq]`)
	for k, p := range catalog {
		if p.zh == "" || p.en == "" {
			t.Errorf("%s: missing translation", k)
		}
		if len(verbs.FindAllString(p.zh, -1)) != len(verbs.FindAllString(p.en, -1)) {
			t.Errorf("%s: verb count differs: %q vs %q", k, p.zh, p.en)
		}
		if strings.Count(p.zh, "<b>") != strings.Count(p.zh, "</b>") || strings.Count(p.en, "<b>") != strings.Count(p.en, "</b>") {
			t.Errorf("%s: unbalanced <b>", k)
		}
	}
}
