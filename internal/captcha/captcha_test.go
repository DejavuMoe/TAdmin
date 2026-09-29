package captcha

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var testSeed = []byte("0123456789abcdef-test-seed")

var allKinds = []Kind{KindImage, KindMath, KindButton}

func mustGenerate(t testing.TB, kind Kind, seed []byte, gen int, lang string) *Challenge {
	t.Helper()
	c, err := Generate(kind, seed, gen, lang)
	if err != nil {
		t.Fatalf("Generate(%s, gen=%d): %v", kind, gen, err)
	}
	return c
}

func TestParseKind(t *testing.T) {
	for in, want := range map[string]Kind{"image": KindImage, " Math ": KindMath, "BUTTON": KindButton} {
		if got, ok := ParseKind(in); !ok || got != want {
			t.Errorf("ParseKind(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "captcha", "images"} {
		if _, ok := ParseKind(in); ok {
			t.Errorf("ParseKind(%q) accepted", in)
		}
	}
}

func TestGenerateRejectsBadInput(t *testing.T) {
	if _, err := Generate(KindImage, []byte("short"), 1, "en"); !errors.Is(err, ErrShortSeed) {
		t.Errorf("short seed: %v", err)
	}
	if _, err := Generate(KindImage, testSeed, 0, "en"); !errors.Is(err, ErrInvalidGeneration) {
		t.Errorf("generation 0: %v", err)
	}
	if _, err := Generate(Kind("nope"), testSeed, 1, "en"); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("unknown kind: %v", err)
	}
}

func TestDeterminism(t *testing.T) {
	for _, k := range allKinds {
		for _, lang := range []string{"zh-CN", "en"} {
			a := mustGenerate(t, k, testSeed, 3, lang)
			b := mustGenerate(t, k, testSeed, 3, lang)
			if a.Prompt != b.Prompt || !bytes.Equal(a.Image, b.Image) ||
				!slices.Equal(a.Options, b.Options) || !slices.Equal(a.answer, b.answer) {
				t.Errorf("%s/%s: output not deterministic", k, lang)
			}
		}
		// The language never changes the content or the answer.
		zh := mustGenerate(t, k, testSeed, 3, "zh-CN")
		en := mustGenerate(t, k, testSeed, 3, "en")
		if !bytes.Equal(zh.Image, en.Image) || !slices.Equal(zh.answer, en.answer) {
			t.Errorf("%s: content depends on language", k)
		}
	}
}

func TestGenerationsDiffer(t *testing.T) {
	for _, k := range []Kind{KindImage, KindMath} {
		seen := map[string]bool{}
		for gen := 1; gen <= 8; gen++ {
			c := mustGenerate(t, k, testSeed, gen, "en")
			key := fmt.Sprint(c.Prompt, c.Options, c.answer)
			if seen[key] {
				t.Errorf("%s: generation %d repeats an earlier challenge", k, gen)
			}
			seen[key] = true
			if gen > 1 {
				prev := mustGenerate(t, k, testSeed, gen-1, "en")
				if bytes.Equal(prev.Image, c.Image) {
					t.Errorf("%s: generations %d and %d share an image", k, gen-1, gen)
				}
			}
		}
		other := mustGenerate(t, k, []byte("fedcba9876543210-other-seed"), 1, "en")
		if bytes.Equal(other.Image, mustGenerate(t, k, testSeed, 1, "en").Image) {
			t.Errorf("%s: different seeds produced the same image", k)
		}
	}
}

func TestCheckImage(t *testing.T) {
	c := mustGenerate(t, KindImage, testSeed, 1, "en")
	if c.Picks != 3 || len(c.answer) != 3 || c.Options != nil {
		t.Fatalf("unexpected image challenge shape: %+v", c)
	}
	good := slices.Clone(c.answer)
	if !c.Check(good) {
		t.Fatal("correct answer rejected")
	}
	reversed := slices.Clone(good)
	slices.Reverse(reversed)
	wrong := slices.Clone(good)
	wrong[2] = firstNotIn(good)
	bad := map[string][]int{
		"nil":          nil,
		"empty":        {},
		"short":        good[:2],
		"long":         append(slices.Clone(good), firstNotIn(good)),
		"reversed":     reversed,
		"wrong cell":   wrong,
		"duplicates":   {good[0], good[0], good[0]},
		"negative":     {-1, good[1], good[2]},
		"out of range": {good[0], good[1], 9},
		"huge":         {good[0], good[1], 1 << 40},
	}
	for name, ans := range bad {
		if c.Check(ans) {
			t.Errorf("%s answer %v accepted", name, ans)
		}
	}
	var nilChallenge *Challenge
	if nilChallenge.Check(good) {
		t.Error("nil challenge accepted an answer")
	}
}

func firstNotIn(xs []int) int {
	for i := 0; ; i++ {
		if !slices.Contains(xs, i) {
			return i
		}
	}
}

func TestCheckOptions(t *testing.T) {
	m := mustGenerate(t, KindMath, testSeed, 1, "en")
	if len(m.Options) != mathOptions || m.Picks != 1 {
		t.Fatalf("math: %d options, picks %d", len(m.Options), m.Picks)
	}
	for i := range m.Options {
		if got, want := m.Check([]int{i}), i == m.answer[0]; got != want {
			t.Errorf("math Check([%d]) = %v, want %v", i, got, want)
		}
	}
	b := mustGenerate(t, KindButton, testSeed, 1, "zh-CN")
	if b.Image != nil || len(b.Options) != 1 || b.Picks != 1 {
		t.Fatalf("button: unexpected shape %+v", b)
	}
	if !b.Check([]int{0}) {
		t.Error("button Check([0]) = false")
	}
	for _, ans := range [][]int{nil, {}, {1}, {-1}, {0, 0}, {6}} {
		if b.Check(ans) {
			t.Errorf("button accepted malformed answer %v", ans)
		}
		if len(ans) > 0 && m.Check(append(slices.Clone(ans), m.answer...)) {
			t.Errorf("math accepted multi-value answer %v", ans)
		}
	}
	if m.Check([]int{mathOptions}) || m.Check([]int{-1}) {
		t.Error("out-of-range option accepted")
	}
}

func TestMathPlans(t *testing.T) {
	for gen := 1; gen <= 2000; gen++ {
		p := planMath(newStream(testSeed, KindMath, gen))
		ans := p.result()
		if ans < 0 || ans > 99 {
			t.Fatalf("gen %d: result %d out of range", gen, ans)
		}
		if p.values[p.correct] != ans {
			t.Fatalf("gen %d: correct option mismatch", gen)
		}
		seen := map[int]bool{}
		for _, v := range p.values {
			if v < 0 || v > 99 || seen[v] {
				t.Fatalf("gen %d: bad options %v", gen, p.values)
			}
			seen[v] = true
		}
	}
}

func TestGridPlans(t *testing.T) {
	for gen := 1; gen <= 2000; gen++ {
		p := planGrid(newStream(testSeed, KindImage, gen))
		pairs := map[[2]int]bool{}
		for i := range p.digits {
			pair := [2]int{int(p.shapes[i]), p.digits[i]}
			if p.digits[i] < 1 || p.digits[i] > 9 || pairs[pair] {
				t.Fatalf("gen %d: duplicate or invalid cell %v", gen, pair)
			}
			pairs[pair] = true
		}
		if !p.ambiguous() {
			t.Fatalf("gen %d: a target is identifiable by digit or shape alone", gen)
		}
		if p.targets[0] == p.targets[1] || p.targets[1] == p.targets[2] || p.targets[0] == p.targets[2] {
			t.Fatalf("gen %d: repeated targets %v", gen, p.targets)
		}
	}
}

// chiSquare returns the chi-square statistic of counts against a uniform
// expectation.
func chiSquare(counts []int) float64 {
	total := 0
	for _, c := range counts {
		total += c
	}
	exp := float64(total) / float64(len(counts))
	var x float64
	for _, c := range counts {
		d := float64(c) - exp
		x += d * d / exp
	}
	return x
}

func TestStreamUniform(t *testing.T) {
	// Critical values at p = 0.001 for df = n-1.
	crit := map[int]float64{6: 20.52, 7: 22.46, 9: 26.12, 10: 27.88}
	for n, limit := range crit {
		s := newStream(testSeed, KindImage, 1)
		counts := make([]int, n)
		for i := 0; i < 60000; i++ {
			v := s.intn(n)
			if v < 0 || v >= n {
				t.Fatalf("intn(%d) = %d", n, v)
			}
			counts[v]++
		}
		if x := chiSquare(counts); x > limit {
			t.Errorf("intn(%d) chi-square %.2f > %.2f: %v", n, x, limit, counts)
		}
	}
	if newStream(testSeed, KindImage, 1).intn(1) != 0 {
		t.Error("intn(1) != 0")
	}
}

func TestAnswerDistribution(t *testing.T) {
	cells := make([]int, gridCells)
	correct := make([]int, mathOptions)
	for gen := 1; gen <= 3000; gen++ {
		p := planGrid(newStream(testSeed, KindImage, gen))
		for _, c := range p.targets {
			cells[c]++
		}
		correct[planMath(newStream(testSeed, KindMath, gen)).correct]++
	}
	if x := chiSquare(cells); x > 26.12 {
		t.Errorf("target cells skewed (chi2 %.2f): %v", x, cells)
	}
	if x := chiSquare(correct); x > 20.52 {
		t.Errorf("correct option index skewed (chi2 %.2f): %v", x, correct)
	}
}

func TestImages(t *testing.T) {
	cases := []struct {
		kind   Kind
		w, h   int
		budget int
	}{
		{KindImage, gridImageSize, gridImageSize, 80 << 10},
		{KindMath, mathImageW, mathImageH, 80 << 10},
	}
	dump := os.Getenv("CAPTCHA_DUMP")
	for _, tc := range cases {
		maxSize := 0
		for gen := 1; gen <= 12; gen++ {
			c := mustGenerate(t, tc.kind, testSeed, gen, "zh-CN")
			img, err := png.Decode(bytes.NewReader(c.Image))
			if err != nil {
				t.Fatalf("%s gen %d: decode: %v", tc.kind, gen, err)
			}
			if b := img.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h {
				t.Errorf("%s: bounds %v, want %dx%d", tc.kind, b, tc.w, tc.h)
			}
			maxSize = max(maxSize, len(c.Image))
			if len(c.Image) > tc.budget {
				t.Errorf("%s gen %d: %d bytes exceeds %d", tc.kind, gen, len(c.Image), tc.budget)
			}
			if dump != "" && gen <= 4 {
				name := filepath.Join(dump, fmt.Sprintf("%s-%d.png", tc.kind, gen))
				if err := os.WriteFile(name, c.Image, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("%s: %s", name, c.Prompt)
			}
		}
		t.Logf("%s: largest PNG %d bytes", tc.kind, maxSize)
	}
}

func TestPrompts(t *testing.T) {
	zh := mustGenerate(t, KindImage, testSeed, 5, "zh-CN")
	en := mustGenerate(t, KindImage, testSeed, 5, "en-US")
	if !strings.HasPrefix(zh.Prompt, "依次点击：") || strings.Count(zh.Prompt, "→") != 2 {
		t.Errorf("zh prompt %q", zh.Prompt)
	}
	if !strings.HasPrefix(en.Prompt, "Tap in order: ") || strings.Count(en.Prompt, "→") != 2 {
		t.Errorf("en prompt %q", en.Prompt)
	}
	// The prompt names shape and digit of each target in order.
	p := planGrid(newStream(testSeed, KindImage, 5))
	for i, c := range p.targets {
		want := fmt.Sprintf("%s %d", text[langEN].shapes[p.shapes[c]], p.digits[c])
		if parts := strings.Split(strings.TrimPrefix(en.Prompt, "Tap in order: "), " → "); parts[i] != want {
			t.Errorf("en prompt part %d = %q, want %q", i, parts[i], want)
		}
	}
	checks := []struct {
		kind Kind
		lang string
		want string
	}{
		{KindMath, "zh-CN", "请计算图中算式，选择正确答案"},
		{KindMath, "en", "Solve the expression in the image and pick the answer"},
		{KindButton, "", "点击下方按钮完成验证"},
		{KindButton, "en", "Tap the button below to verify"},
	}
	for _, tc := range checks {
		if got := mustGenerate(t, tc.kind, testSeed, 1, tc.lang).Prompt; got != tc.want {
			t.Errorf("%s/%s prompt %q, want %q", tc.kind, tc.lang, got, tc.want)
		}
	}
	if got := mustGenerate(t, KindButton, testSeed, 1, "en").Options[0]; got != "✅ I'm human" {
		t.Errorf("en button label %q", got)
	}
	if got := mustGenerate(t, KindButton, testSeed, 1, "zh-CN").Options[0]; got != "✅ 我不是机器人" {
		t.Errorf("zh button label %q", got)
	}
}

func FuzzCheck(f *testing.F) {
	var challenges []*Challenge
	for _, k := range allKinds {
		challenges = append(challenges, mustGenerate(f, k, testSeed, 1, "en"))
	}
	f.Add([]byte{0, 1, 2})
	f.Add([]byte{0})
	f.Add([]byte{255, 255, 255, 255})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		ans := make([]int, len(data))
		for i, b := range data {
			ans[i] = int(int8(b))
		}
		for _, c := range challenges {
			ok := c.Check(ans)
			if ok && !slices.Equal(ans, c.answer) {
				t.Fatalf("%s accepted %v, answer %v", c.Kind, ans, c.answer)
			}
		}
	})
}

func BenchmarkGenerateImage(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		if _, err := Generate(KindImage, testSeed, i+1, "zh-CN"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenerateMath(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		if _, err := Generate(KindMath, testSeed, i+1, "zh-CN"); err != nil {
			b.Fatal(err)
		}
	}
}
