package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedFiles(t *testing.T) {
	total := 0
	for _, name := range []string{"index.html", "app.css", "app.js"} {
		b, err := fs.ReadFile(FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(b) == 0 {
			t.Errorf("%s is empty", name)
		}
		total += len(b)
	}
	if total >= 60<<10 {
		t.Errorf("assets total %d bytes, budget is 60 KiB", total)
	}
}

func TestIndexReferences(t *testing.T) {
	b, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, want := range []string{
		`href="/assets/app.css"`,
		`src="/assets/app.js"`,
		`src="https://telegram.org/js/telegram-web-app.js"`,
		`viewport-fit=cover`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	// No third-party resources other than the official Telegram script.
	for _, m := range regexp.MustCompile(`(?:src|href)="(https?:[^"]*)"`).FindAllStringSubmatch(html, -1) {
		if m[1] != "https://telegram.org/js/telegram-web-app.js" {
			t.Errorf("unexpected external resource %s", m[1])
		}
	}
}

func TestNoExternalFetches(t *testing.T) {
	for _, name := range []string{"app.css", "app.js"} {
		b, err := fs.ReadFile(FS, name)
		if err != nil {
			t.Fatal(err)
		}
		if s := string(b); strings.Contains(s, "http://") || strings.Contains(s, "https://") || strings.Contains(s, "@import") {
			t.Errorf("%s references external resources", name)
		}
	}
	js, _ := fs.ReadFile(FS, "app.js")
	if strings.Contains(string(js), "console.") {
		t.Error("app.js must not log (initData must never reach the console)")
	}
}
