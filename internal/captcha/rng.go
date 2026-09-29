package captcha

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
)

// streamLabel domain-separates the key stream from any other use of the seed.
const streamLabel = "tadmin/captcha/v1"

// stream is a deterministic byte stream: block i is
// HMAC-SHA256(seed, label || 0 || kind || 0 || generation || i), with
// generation and i encoded as 64-bit big-endian integers.
type stream struct {
	mac    hash.Hash
	prefix []byte
	ctr    uint64
	buf    [sha256.Size]byte
	off    int
}

func newStream(seed []byte, kind Kind, generation int) *stream {
	prefix := make([]byte, 0, len(streamLabel)+len(kind)+2+8)
	prefix = append(prefix, streamLabel...)
	prefix = append(prefix, 0)
	prefix = append(prefix, kind...)
	prefix = append(prefix, 0)
	prefix = binary.BigEndian.AppendUint64(prefix, uint64(generation))
	return &stream{
		mac:    hmac.New(sha256.New, seed),
		prefix: prefix,
		off:    sha256.Size,
	}
}

func (s *stream) refill() {
	var ctr [8]byte
	binary.BigEndian.PutUint64(ctr[:], s.ctr)
	s.ctr++
	s.mac.Reset()
	s.mac.Write(s.prefix)
	s.mac.Write(ctr[:])
	s.mac.Sum(s.buf[:0])
	s.off = 0
}

func (s *stream) uint64() uint64 {
	if s.off+8 > len(s.buf) {
		s.refill()
	}
	v := binary.BigEndian.Uint64(s.buf[s.off:])
	s.off += 8
	return v
}

// intn returns a uniform integer in [0, n) using rejection sampling, so the
// result carries no modulo bias. It panics if n <= 0.
func (s *stream) intn(n int) int {
	if n <= 0 {
		panic("captcha: intn with non-positive bound")
	}
	un := uint64(n)
	// 2^64 mod n; values above math.MaxUint64-excess would bias the result.
	excess := (math.MaxUint64%un + 1) % un
	for {
		v := s.uint64()
		if v <= math.MaxUint64-excess {
			return int(v % un)
		}
	}
}

// between returns a uniform integer in [lo, hi].
func (s *stream) between(lo, hi int) int {
	return lo + s.intn(hi-lo+1)
}

// float returns a uniform float64 in [0, 1).
func (s *stream) float() float64 {
	return float64(s.uint64()>>11) / (1 << 53)
}

// rangeF returns a uniform float64 in [lo, hi).
func (s *stream) rangeF(lo, hi float64) float64 {
	return lo + (hi-lo)*s.float()
}

// shuffle performs an unbiased Fisher-Yates shuffle.
func (s *stream) shuffle(n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
		swap(i, s.intn(i+1))
	}
}
