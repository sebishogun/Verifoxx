//go:build !purego

package simdops

import (
	"unsafe"

	"github.com/sebishogun/simd"
)

const pureGo = false

var nativeLittleEndian = func() bool {
	value := uint16(1)
	return *(*byte)(unsafe.Pointer(&value)) == 1
}()

func u32View[T ~uint32](values []T) []uint32 {
	return unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(values))), len(values))
}

func i32View[T ~uint32](values []T) []int32 {
	return unsafe.Slice((*int32)(unsafe.Pointer(unsafe.SliceData(values))), len(values))
}

func i64View[T ~int64](values []T) []int64 {
	return unsafe.Slice((*int64)(unsafe.Pointer(unsafe.SliceData(values))), len(values))
}

func wordBytes(words []uint64) []byte {
	const bytesPerWord = int(unsafe.Sizeof(uint64(0)))
	if len(words) > int(^uint(0)>>1)/bytesPerWord {
		panic("simdops: word slice too large")
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(words))), len(words)*bytesPerWord)
}

func boolBytes(values []bool) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(values))), len(values))
}

// CompareU32 compares every source element with value into dst.
func CompareU32[T ~uint32](dst []bool, src []T, value T, comparison Comparison) {
	if !comparison.Valid() {
		panic(invalidComparisonPanic)
	}
	values := u32View(src)
	switch comparison {
	case Equal:
		simd.EqualScalarInto(dst, values, uint32(value))
	case NotEqual:
		simd.NotEqualScalarInto(dst, values, uint32(value))
	case Less:
		simd.LessScalarInto(dst, values, uint32(value))
	case LessEqual:
		simd.LessEqualScalarInto(dst, values, uint32(value))
	case Greater:
		simd.GreaterScalarInto(dst, values, uint32(value))
	case GreaterEqual:
		simd.GreaterEqualScalarInto(dst, values, uint32(value))
	}
}

// CompareI64 compares every source element with value into dst.
func CompareI64[T ~int64](dst []bool, src []T, value T, comparison Comparison) {
	if !comparison.Valid() {
		panic(invalidComparisonPanic)
	}
	values := i64View(src)
	switch comparison {
	case Equal:
		simd.EqualScalarInto(dst, values, int64(value))
	case NotEqual:
		simd.NotEqualScalarInto(dst, values, int64(value))
	case Less:
		simd.LessScalarInto(dst, values, int64(value))
	case LessEqual:
		simd.LessEqualScalarInto(dst, values, int64(value))
	case Greater:
		simd.GreaterScalarInto(dst, values, int64(value))
	case GreaterEqual:
		simd.GreaterEqualScalarInto(dst, values, int64(value))
	}
}

func AndWords(dst, a, b []uint64)    { simd.AndInto(wordBytes(dst), wordBytes(a), wordBytes(b)) }
func OrWords(dst, a, b []uint64)     { simd.OrInto(wordBytes(dst), wordBytes(a), wordBytes(b)) }
func XorWords(dst, a, b []uint64)    { simd.XorInto(wordBytes(dst), wordBytes(a), wordBytes(b)) }
func AndNotWords(dst, a, b []uint64) { simd.AndNotInto(wordBytes(dst), wordBytes(a), wordBytes(b)) }

func AndMask(dst, a, b []bool) { simd.AndMaskInto(dst, a, b) }
func OrMask(dst, a, b []bool)  { simd.OrMaskInto(dst, a, b) }
func XorMask(dst, a, b []bool) { simd.XorMaskInto(dst, a, b) }
func NotMask(dst, src []bool)  { simd.NotMaskInto(dst, src) }

// PackMask packs one Boolean per row into least-significant-bit-first words.
func PackMask(dst []uint64, src []bool) {
	words := maskWordCount(len(src))
	if len(dst) < words {
		panic(shortMaskDestinationPanic)
	}
	if words == 0 {
		return
	}
	dst = dst[:words:words]
	if !nativeLittleEndian {
		packMaskPortable(dst, src)
		return
	}
	dst[words-1] = 0
	simd.MaskBits(wordBytes(dst), boolBytes(src), 1)
}

// CompressU32 stably packs selected values into dst and returns the count.
func CompressU32[T ~uint32](dst, src []T, mask []bool) int {
	return simd.CompressInto(i32View(dst), i32View(src), mask)
}
