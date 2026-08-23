//go:build purego

package simdops

const pureGo = true

// CompareU32 compares every source element with value into dst.
func CompareU32[T ~uint32](dst []bool, src []T, value T, comparison Comparison) {
	if !comparison.Valid() {
		panic(invalidComparisonPanic)
	}
	n := min(len(dst), len(src))
	switch comparison {
	case Equal:
		for i := range n {
			dst[i] = src[i] == value
		}
	case NotEqual:
		for i := range n {
			dst[i] = src[i] != value
		}
	case Less:
		for i := range n {
			dst[i] = src[i] < value
		}
	case LessEqual:
		for i := range n {
			dst[i] = src[i] <= value
		}
	case Greater:
		for i := range n {
			dst[i] = src[i] > value
		}
	case GreaterEqual:
		for i := range n {
			dst[i] = src[i] >= value
		}
	}
}

// CompareI64 compares every source element with value into dst.
func CompareI64[T ~int64](dst []bool, src []T, value T, comparison Comparison) {
	if !comparison.Valid() {
		panic(invalidComparisonPanic)
	}
	n := min(len(dst), len(src))
	switch comparison {
	case Equal:
		for i := range n {
			dst[i] = src[i] == value
		}
	case NotEqual:
		for i := range n {
			dst[i] = src[i] != value
		}
	case Less:
		for i := range n {
			dst[i] = src[i] < value
		}
	case LessEqual:
		for i := range n {
			dst[i] = src[i] <= value
		}
	case Greater:
		for i := range n {
			dst[i] = src[i] > value
		}
	case GreaterEqual:
		for i := range n {
			dst[i] = src[i] >= value
		}
	}
}

func AndWords(dst, a, b []uint64) {
	for i := range min(len(dst), len(a), len(b)) {
		dst[i] = a[i] & b[i]
	}
}

func OrWords(dst, a, b []uint64) {
	for i := range min(len(dst), len(a), len(b)) {
		dst[i] = a[i] | b[i]
	}
}

func XorWords(dst, a, b []uint64) {
	for i := range min(len(dst), len(a), len(b)) {
		dst[i] = a[i] ^ b[i]
	}
}

func AndNotWords(dst, a, b []uint64) {
	for i := range min(len(dst), len(a), len(b)) {
		dst[i] = a[i] &^ b[i]
	}
}

func AndMask(dst, a, b []bool) {
	for i := range min(len(dst), len(a), len(b)) {
		dst[i] = a[i] && b[i]
	}
}

func OrMask(dst, a, b []bool) {
	for i := range min(len(dst), len(a), len(b)) {
		dst[i] = a[i] || b[i]
	}
}

func XorMask(dst, a, b []bool) {
	for i := range min(len(dst), len(a), len(b)) {
		dst[i] = a[i] != b[i]
	}
}

func NotMask(dst, src []bool) {
	for i := range min(len(dst), len(src)) {
		dst[i] = !src[i]
	}
}

// PackMask packs one Boolean per row into least-significant-bit-first words.
func PackMask(dst []uint64, src []bool) {
	packMaskPortable(dst, src)
}

// CompressU32 stably packs selected values into dst and returns the count.
func CompressU32[T ~uint32](dst, src []T, mask []bool) int {
	n := min(len(src), len(mask))
	written := 0
	for i := range n {
		if !mask[i] {
			continue
		}
		if written == len(dst) {
			return written
		}
		dst[written] = src[i]
		written++
	}
	return written
}
