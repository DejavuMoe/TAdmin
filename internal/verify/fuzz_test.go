package verify

import (
	"math"
	"testing"
	"time"
)

func FuzzInitDataParser(f *testing.F) {
	for _, raw := range []string{"", "hash=%xx", "user=%7B%7D&hash=00", "auth_date=1&auth_date=2", "user=\x00"} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = ValidateInitData(raw, "123:synthetic-fuzz-token", time.Unix(1789570000, 0), DefaultInitDataMaxAge, DefaultFutureSkew)
	})
}
func FuzzChallengeCoordinates(f *testing.F) {
	f.Add(0.1, 0.1, 0.9, 0.9)
	f.Add(math.Inf(1), math.NaN(), -1.0, 1.0)
	challenge, _ := NewChallenge(make([]byte, 32), 1)
	f.Fuzz(func(t *testing.T, x, y, a, b float64) {
		ok, err := challenge.Check([]Point{{X: x, Y: y}, {X: a, Y: b}})
		for _, n := range []float64{x, y, a, b} {
			if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n >= 1 {
				if err == nil || ok {
					t.Fatal("invalid coordinate accepted")
				}
				return
			}
		}
	})
}
