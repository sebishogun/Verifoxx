// Package simdops isolates the policy engine from the concrete SIMD library.
package simdops

// Comparison selects one scalar comparison for a whole typed column.
type Comparison uint8

const (
	Equal Comparison = iota + 1
	NotEqual
	Less
	LessEqual
	Greater
	GreaterEqual
)

// Valid reports whether comparison names a supported operation.
func (comparison Comparison) Valid() bool {
	return comparison >= Equal && comparison <= GreaterEqual
}

const invalidComparisonPanic = "simdops: invalid comparison"

const shortMaskDestinationPanic = "simdops: mask destination too short"

func maskWordCount(rows int) int {
	words := rows / 64
	if rows&63 != 0 {
		words++
	}
	return words
}

func packMaskPortable(dst []uint64, src []bool) {
	words := maskWordCount(len(src))
	if len(dst) < words {
		panic(shortMaskDestinationPanic)
	}
	clear(dst[:words])
	for row, set := range src {
		if set {
			dst[row>>6] |= uint64(1) << (uint(row) & 63)
		}
	}
}
