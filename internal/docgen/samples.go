package docgen

import (
	"crypto/sha256"
	"fmt"

	"tadmin/internal/captcha"
)

type sample struct {
	Kind     string   `json:"kind"`
	Image    string   `json:"image,omitempty"`
	PromptZH string   `json:"prompt_zh"`
	PromptEN string   `json:"prompt_en"`
	Options  []string `json:"options,omitempty"`
	Picks    int      `json:"picks"`
	Answer   []int    `json:"answer"`
}

type samplesOut struct {
	Geometry captcha.Geometry `json:"geometry"`
	Samples  []sample         `json:"samples"`
}

// captchaSamples renders a few real challenges with fixed seeds for the
// interactive playground. Answers are recovered by brute force through the
// public Check method, exactly as a solver would.
func captchaSamples() (samplesOut, map[string][]byte, error) {
	out := samplesOut{Geometry: captcha.GridGeometry()}
	images := map[string][]byte{}
	plan := []struct {
		kind captcha.Kind
		n    int
	}{{captcha.KindImage, 4}, {captcha.KindMath, 3}}
	for _, p := range plan {
		for i := 1; i <= p.n; i++ {
			seed := sha256.Sum256([]byte(fmt.Sprintf("tadmin-docs-%s-%d", p.kind, i)))
			zh, err := captcha.Generate(p.kind, seed[:], 1, "zh-CN")
			if err != nil {
				return out, nil, err
			}
			en, err := captcha.Generate(p.kind, seed[:], 1, "en")
			if err != nil {
				return out, nil, err
			}
			ans := solve(zh)
			if ans == nil {
				return out, nil, fmt.Errorf("sample %s-%d has no solution", p.kind, i)
			}
			name := fmt.Sprintf("%s-%d.png", p.kind, i)
			images[name] = zh.Image
			out.Samples = append(out.Samples, sample{
				Kind: string(p.kind), Image: "/samples/" + name, PromptZH: zh.Prompt, PromptEN: en.Prompt,
				Options: zh.Options, Picks: zh.Picks, Answer: ans,
			})
		}
	}
	return out, images, nil
}

func solve(c *captcha.Challenge) []int {
	if c.Kind != captcha.KindImage {
		for i := range c.Options {
			if c.Check([]int{i}) {
				return []int{i}
			}
		}
		return nil
	}
	n := captcha.GridSize * captcha.GridSize
	for a := 0; a < n; a++ {
		for b := 0; b < n; b++ {
			for d := 0; d < n; d++ {
				if a != b && b != d && a != d && c.Check([]int{a, b, d}) {
					return []int{a, b, d}
				}
			}
		}
	}
	return nil
}
