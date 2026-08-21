package truth

import (
	"fmt"
	"testing"
)

// sinkWord defeats dead-code elimination: every timed kernel writes into dst,
// which is read here after the timer stops, so the compiler cannot drop the
// writes from the measured loop.
var sinkWord uint64

// fillPlane returns an exact-sized plane of deterministic nonzero words. For a
// partial final word (rows not a multiple of 64) the unused tail bits are
// poisoned to 1 so dirty tails are exercised like the tests' buildPlanes.
func fillPlane(words int, rows uint32) []uint64 {
	p := make([]uint64, words)
	for i := range p {
		p[i] = uint64(i+1)*0x9e3779b97f4a7c15 + 0x1234567890abcdef
	}
	if rem := rows & 63; rem != 0 {
		p[words-1] |= ^((uint64(1) << rem) - 1)
	}
	return p
}

// BenchmarkTruth measures steady-state word traffic of the three bitplane
// kernels. Per-op bytes count loop traffic: Not moves 4 words per word
// (2 loads + 2 stores), And/Or move 6 (4 loads + 2 stores). Partial-row byte
// counts exclude the fixed final-word read-modify-writes in maskTail.
func BenchmarkTruth(b *testing.B) {
	rowsCases := []uint32{64, 65, 1_024, 8_192}
	for _, rows := range rowsCases {
		words := WordCount(rows)
		src := Planes{Positive: fillPlane(words, rows), Negative: fillPlane(words, rows)}
		left := Planes{Positive: fillPlane(words, rows), Negative: fillPlane(words, rows)}
		right := Planes{Positive: fillPlane(words, rows), Negative: fillPlane(words, rows)}
		dst := Planes{Positive: fillPlane(words, rows), Negative: fillPlane(words, rows)}
		b.Run(fmt.Sprintf("Not/Rows%d", rows), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(words) * 32)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				Not(dst, src, rows)
			}
			b.StopTimer()
			sinkWord = dst.Positive[0] ^ dst.Negative[0]
		})
		b.Run(fmt.Sprintf("And/Rows%d", rows), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(words) * 48)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				And(dst, left, right, rows)
			}
			b.StopTimer()
			sinkWord = dst.Positive[0] ^ dst.Negative[0]
		})
		b.Run(fmt.Sprintf("Or/Rows%d", rows), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(words) * 48)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				Or(dst, left, right, rows)
			}
			b.StopTimer()
			sinkWord = dst.Positive[0] ^ dst.Negative[0]
		})
	}
}
