package docgen

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"tadmin/internal/captcha"
	"tadmin/internal/config"
)

const root = "../.."

// TestGeneratedDocsUpToDate keeps the documentation site honest: any change
// to configuration, commands, schema, tests, captcha rendering or the
// progress file must be followed by `go run ./cmd/docgen`.
func TestGeneratedDocsUpToDate(t *testing.T) {
	files, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	if stale := Stale(root, files); len(stale) > 0 {
		t.Fatalf("documentation data is stale; run `go run ./cmd/docgen`:\n%v", stale)
	}
}

// TestMiniAppGridMatchesRenderer guards the tap-target overlay in the Mini
// App against layout changes in the captcha renderer.
func TestMiniAppGridMatchesRenderer(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(root, "assets/web/app.js"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const GRID = \{ size: (\d+), margin: (\d+), cell: (\d+), gap: (\d+), n: (\d+) \}`).FindSubmatch(src)
	if m == nil {
		t.Fatal("GRID constant not found in app.js")
	}
	atoi := func(b []byte) int { n, _ := strconv.Atoi(string(b)); return n }
	g := captcha.GridGeometry()
	got := captcha.Geometry{Size: atoi(m[1]), Margin: atoi(m[2]), Cell: atoi(m[3]), Gap: atoi(m[4]), N: atoi(m[5])}
	if got != g {
		t.Fatalf("app.js GRID %+v != renderer %+v", got, g)
	}
}

// TestConfigExampleMatchesSample keeps the committed example config (which
// the docs embed) identical to `tadmin sample-config`.
func TestConfigExampleMatchesSample(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(root, "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(string(data), "\r\n", "\n")
	if got != config.Sample {
		t.Fatal("config.example.toml differs from config.Sample; run `go run ./cmd/tadmin sample-config > config.example.toml`")
	}
}
