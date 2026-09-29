// Package docgen extracts facts from the code base into JSON (and sample
// images) consumed by the VitePress documentation site, so the docs cannot
// silently drift from the implementation. `go run ./cmd/docgen` writes the
// files; the package test fails when they are stale.
package docgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"tadmin/internal/bot"
	"tadmin/internal/captcha"
	"tadmin/internal/config"
	"tadmin/internal/gate"
	"tadmin/internal/i18n"
	"tadmin/internal/store"
	"tadmin/internal/tg"
)

// DataDir and SampleDir are the output locations relative to the repo root.
const (
	DataDir   = "docs/.vitepress/data/generated"
	SampleDir = "docs/public/samples"
)

// Generate returns every generated file keyed by its slash-separated path
// relative to root.
func Generate(root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	put := func(name string, v any) error {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		out[DataDir+"/"+name] = append(data, '\n')
		return nil
	}

	tests, err := collectTests(root)
	if err != nil {
		return nil, err
	}
	progress, err := loadProgress(root, tests)
	if err != nil {
		return nil, err
	}
	schema, err := loadSchema(root)
	if err != nil {
		return nil, err
	}
	methods, err := botAPIMethods(root)
	if err != nil {
		return nil, err
	}
	samples, images, err := captchaSamples()
	if err != nil {
		return nil, err
	}
	for name, png := range images {
		out[SampleDir+"/"+name] = png
	}
	meta, err := loadMeta(root, tests, methods, schema, progress)
	if err != nil {
		return nil, err
	}

	settings, err := settingsDoc()
	if err != nil {
		return nil, err
	}
	steps := []error{
		put("meta.json", meta),
		put("config.json", configDoc()),
		put("settings.json", settings),
		put("verify.json", verifyDoc()),
		put("commands.json", commandsDoc()),
		put("botapi.json", methods),
		put("schema.json", schema),
		put("tests.json", tests),
		put("captcha.json", samples),
		put("progress.json", progress),
	}
	for _, err := range steps {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ---- config ----

type configOut struct {
	Env    []config.EnvVar `json:"env"`
	Sample string          `json:"sample"`
}

func configDoc() configOut {
	return configOut{Env: config.EnvVars, Sample: config.Sample}
}

// ---- verification facts ----

type cooldownStep struct {
	Failure int    `json:"failure"`
	Seconds int64  `json:"seconds"`
	Label   string `json:"label"`
}

type verifyOut struct {
	Cooldown   []cooldownStep    `json:"cooldown"`
	MinBan     int64             `json:"min_ban_seconds"`
	Geometry   captcha.Geometry  `json:"geometry"`
	BlindGuess map[string]string `json:"blind_guess"`
}

func verifyDoc() verifyOut {
	v := verifyOut{MinBan: int64(gate.MinBan.Seconds()), Geometry: captcha.GridGeometry(),
		BlindGuess: map[string]string{"image": "1/504", "math": "1/6", "button": "—"}}
	for n := 1; n <= 7; n++ {
		d := gate.Cooldown(n)
		v.Cooldown = append(v.Cooldown, cooldownStep{Failure: n, Seconds: int64(d.Seconds()), Label: shortDuration(d)})
	}
	return v
}

func shortDuration(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", d/time.Hour, (d%time.Hour)/time.Minute)
	}
	return fmt.Sprintf("%dm", d/time.Minute)
}

// ---- commands ----

type menus struct {
	Private []tg.BotCommand `json:"private"`
	Admins  []tg.BotCommand `json:"admins"`
}

func commandsDoc() map[string]menus {
	out := map[string]menus{}
	for _, lang := range []string{i18n.ZH, i18n.EN} {
		p, a := bot.CommandMenus(lang)
		out[lang] = menus{Private: p, Admins: a}
	}
	return out
}

// ---- Bot API methods ----

type apiMethod struct {
	Method string `json:"method"`
	Func   string `json:"func"`
}

var callRe = regexp.MustCompile(`func \(c \*Client\) (\w+)\([^)]*\)[^{]*\{(?s:.*?)c\.(?:Call|CallUpload|do)\(ctx, "(\w+)"`)

func botAPIMethods(root string) ([]apiMethod, error) {
	src, err := os.ReadFile(filepath.Join(root, "internal/tg/methods.go"))
	if err != nil {
		return nil, err
	}
	var out []apiMethod
	seen := map[string]bool{}
	for _, m := range callRe.FindAllSubmatch(src, -1) {
		name := string(m[2])
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, apiMethod{Method: name, Func: string(m[1])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Method < out[j].Method })
	return out, nil
}

// ---- schema ----

type column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type table struct {
	Name    string   `json:"name"`
	Columns []column `json:"columns"`
}

var createRe = regexp.MustCompile(`(?s)CREATE TABLE (\w+) \((.*?)\n\);`)

func loadSchema(root string) ([]table, error) {
	files, err := filepath.Glob(filepath.Join(root, "assets/migrations/*.sql"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []table
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(string(src), "\r\n", "\n")
		for _, m := range createRe.FindAllStringSubmatch(text, -1) {
			t := table{Name: m[1]}
			for _, line := range strings.Split(m[2], "\n") {
				f := strings.Fields(strings.TrimSuffix(strings.TrimSpace(line), ","))
				if len(f) < 2 || f[0] == "PRIMARY" || f[0] == "UNIQUE" {
					continue
				}
				t.Columns = append(t.Columns, column{Name: f[0], Type: f[1]})
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// ---- tests ----

type testPkg struct {
	Package string   `json:"package"`
	Tests   []string `json:"tests"`
}

type testsOut struct {
	Total    int       `json:"total"`
	Packages []testPkg `json:"packages"`
	index    map[string]string
}

func collectTests(root string) (testsOut, error) {
	out := testsOut{index: map[string]string{}}
	byPkg := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "dist", "bin", "data":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		pkg := filepath.ToSlash(rel)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
				continue
			}
			byPkg[pkg] = append(byPkg[pkg], fn.Name.Name)
			out.index[fn.Name.Name] = pkg
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	for pkg, names := range byPkg {
		sort.Strings(names)
		out.Packages = append(out.Packages, testPkg{Package: pkg, Tests: names})
		out.Total += len(names)
	}
	sort.Slice(out.Packages, func(i, j int) bool {
		if len(out.Packages[i].Tests) != len(out.Packages[j].Tests) {
			return len(out.Packages[i].Tests) > len(out.Packages[j].Tests)
		}
		return out.Packages[i].Package < out.Packages[j].Package
	})
	return out, nil
}

// ---- progress ----

type testRef struct {
	Name    string `json:"name"`
	Package string `json:"package"`
}

type feature struct {
	Area   string    `toml:"area" json:"area"`
	Title  string    `toml:"title" json:"title"`
	Status string    `toml:"status" json:"status"`
	Tests  []string  `toml:"tests" json:"-"`
	Refs   []testRef `toml:"-" json:"tests"`
	Live   string    `toml:"live" json:"live,omitempty"`
}

type milestone struct {
	Title  string `toml:"title" json:"title"`
	Date   string `toml:"date" json:"date,omitempty"`
	Status string `toml:"status" json:"status"`
	Desc   string `toml:"desc" json:"desc"`
}

type progressOut struct {
	Version    string         `toml:"version" json:"version"`
	Updated    string         `toml:"updated" json:"updated"`
	Headline   string         `toml:"headline" json:"headline"`
	Milestones []milestone    `toml:"milestone" json:"milestones"`
	Features   []feature      `toml:"feature" json:"features"`
	Counts     map[string]int `toml:"-" json:"counts"`
}

var featureStatuses = map[string]bool{"verified": true, "live": true, "shipped": true, "planned": true}
var milestoneStatuses = map[string]bool{"done": true, "next": true, "planned": true}

func loadProgress(root string, tests testsOut) (progressOut, error) {
	var p progressOut
	data, err := os.ReadFile(filepath.Join(root, "docs/progress.toml"))
	if err != nil {
		return p, err
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("docs/progress.toml: %w", err)
	}
	p.Counts = map[string]int{}
	var problems []string
	for i := range p.Features {
		f := &p.Features[i]
		if !featureStatuses[f.Status] {
			problems = append(problems, fmt.Sprintf("feature %q: unknown status %q", f.Title, f.Status))
		}
		if (f.Status == "verified" || f.Status == "shipped") && len(f.Tests) == 0 {
			problems = append(problems, fmt.Sprintf("feature %q is %s but lists no tests", f.Title, f.Status))
		}
		for _, name := range f.Tests {
			pkg, ok := tests.index[name]
			if !ok {
				problems = append(problems, fmt.Sprintf("feature %q references missing test %s", f.Title, name))
				continue
			}
			f.Refs = append(f.Refs, testRef{Name: name, Package: pkg})
		}
		if f.Refs == nil {
			f.Refs = []testRef{}
		}
		p.Counts[f.Status]++
	}
	for _, m := range p.Milestones {
		if !milestoneStatuses[m.Status] {
			problems = append(problems, fmt.Sprintf("milestone %q: unknown status %q", m.Title, m.Status))
		}
	}
	if len(problems) > 0 {
		return p, fmt.Errorf("docs/progress.toml:\n  %s", strings.Join(problems, "\n  "))
	}
	return p, nil
}

// ---- meta ----

type metaOut struct {
	Version   string            `json:"version"`
	Updated   string            `json:"updated"`
	GoVersion string            `json:"go_version"`
	Toolchain string            `json:"toolchain"`
	Deps      []string          `json:"deps"`
	Counts    map[string]int    `json:"counts"`
	Packages  []string          `json:"packages"`
	Delivery  []string          `json:"delivery"`
	Kinds     []string          `json:"kinds"`
	Languages []string          `json:"languages"`
	Sources   map[string]string `json:"sources"`
}

var requireRe = regexp.MustCompile(`(?m)^\t([\w./-]+) (v[\w.+-]+)$`)

func loadMeta(root string, tests testsOut, methods []apiMethod, schema []table, p progressOut) (metaOut, error) {
	m := metaOut{Version: p.Version, Updated: p.Updated, Counts: map[string]int{},
		Delivery:  []string{store.DeliveryOverlay, store.DeliveryPublic, store.DeliveryPrivate},
		Kinds:     []string{string(captcha.KindImage), string(captcha.KindMath), string(captcha.KindButton)},
		Languages: []string{i18n.ZH, i18n.EN},
		Sources:   map[string]string{},
	}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return m, err
	}
	text := strings.ReplaceAll(string(gomod), "\r\n", "\n")
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "go ") {
			m.GoVersion = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	// Direct dependencies are those without an "// indirect" marker.
	for _, match := range requireRe.FindAllStringSubmatch(text, -1) {
		m.Deps = append(m.Deps, match[1]+" "+match[2])
	}
	if mise, err := os.ReadFile(filepath.Join(root, "mise.toml")); err == nil {
		var tools struct {
			Tools map[string]string `toml:"tools"`
		}
		if toml.Unmarshal(mise, &tools) == nil {
			m.Toolchain = tools.Tools["go"]
		}
	}
	pkgs, err := goPackages(root)
	if err != nil {
		return m, err
	}
	m.Packages = pkgs
	m.Counts["tests"] = tests.Total
	m.Counts["test_packages"] = len(tests.Packages)
	m.Counts["bot_api_methods"] = len(methods)
	m.Counts["tables"] = len(schema)
	m.Counts["i18n_keys"] = len(i18n.Keys())
	m.Counts["env_vars"] = len(config.EnvVars)
	m.Counts["packages"] = len(pkgs)
	sort.Strings(m.Deps)
	return m, nil
}

func goPackages(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if matches, _ := filepath.Glob(filepath.Join(path, "*.go")); len(matches) > 0 {
			rel, _ := filepath.Rel(root, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// Stale lists generated files whose on-disk content differs. Text files are
// compared with line endings normalized (Git may check them out as CRLF).
func Stale(root string, files map[string][]byte) []string {
	var out []string
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			out = append(out, name)
			continue
		}
		if strings.HasSuffix(name, ".json") {
			got = bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
		}
		if !bytes.Equal(got, want) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
