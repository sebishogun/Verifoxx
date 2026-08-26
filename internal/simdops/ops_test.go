package simdops_test

import (
	"slices"
	"testing"

	"github.com/sebishogun/nornrune/internal/simdops"
	"github.com/sebishogun/simd"
)

type testID uint32

func TestOperationsMatchScalar(t *testing.T) {
	lengths := [...]int{0, 1, 3, 4, 5, 15, 16, 17, 31, 32, 33, 63, 64, 65, 191, 192, 193}
	comparisons := [...]simdops.Comparison{
		simdops.Equal,
		simdops.NotEqual,
		simdops.Less,
		simdops.LessEqual,
		simdops.Greater,
		simdops.GreaterEqual,
	}
	for _, n := range lengths {
		t.Run(testName(n), func(t *testing.T) {
			testComparisons(t, n, comparisons[:])
			testWordOperations(t, n)
			testMaskOperations(t, n)
			testMaskPacking(t, n)
			testCompression(t, n)
		})
	}
	testShortestInputs(t)

	info := simdops.Runtime()
	if info.Tier == "" || info.Description == "" {
		t.Fatalf("Runtime() = %+v", info)
	}
	compareThreshold := max(
		simd.KernelThreshold("EqualScalarMask"),
		simd.KernelThreshold("NotEqualScalarMask"),
		simd.KernelThreshold("LessScalarMask"),
		simd.KernelThreshold("LessEqualScalarMask"),
		simd.KernelThreshold("GreaterScalarMask"),
		simd.KernelThreshold("GreaterEqualScalarMask"),
	)
	bitwiseBytes := max(
		simd.KernelThreshold("And"),
		simd.KernelThreshold("Or"),
		simd.KernelThreshold("Xor"),
		simd.KernelThreshold("AndNot"),
	)
	wantThresholds := simdops.Thresholds{
		CompareU32:  compareThreshold,
		CompareI64:  compareThreshold,
		WordBitwise: (bitwiseBytes + 7) / 8,
		BoolBitwise: max(
			simd.KernelThreshold("All"),
			simd.KernelThreshold("Any"),
			simd.KernelThreshold("Count"),
		),
		CompressU32: simd.KernelThreshold("Compress"),
		PackMask:    simd.KernelThreshold("MaskBits"),
	}
	if info.Thresholds != wantThresholds {
		t.Fatalf("Runtime().Thresholds = %+v want %+v", info.Thresholds, wantThresholds)
	}

	dst := []bool{true, false, true}
	want := slices.Clone(dst)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("invalid comparison did not panic")
			}
		}()
		simdops.CompareU32(dst, []testID{1, 2, 3}, 2, 0)
	}()
	if !slices.Equal(dst, want) {
		t.Fatalf("invalid comparison mutated dst: got %v want %v", dst, want)
	}

	ids := make([]testID, 256)
	mask := make([]bool, len(ids))
	wordsA := make([]uint64, 4)
	wordsB := make([]uint64, 4)
	wordsDst := make([]uint64, 4)
	packed := make([]uint64, 4)
	compressed := make([]testID, len(ids))
	simdops.CompareU32(mask, ids, 0, simdops.Equal)
	if allocs := testing.AllocsPerRun(100, func() {
		simdops.CompareU32(mask, ids, 0, simdops.Equal)
		simdops.AndWords(wordsDst, wordsA, wordsB)
		simdops.PackMask(packed, mask)
		simdops.CompressU32(compressed, ids, mask)
	}); allocs != 0 {
		t.Fatalf("warm wrappers allocate: %.2f allocs/run", allocs)
	}
}

func testShortestInputs(t *testing.T) {
	t.Helper()

	comparisonDst := []bool{false, false, true}
	simdops.CompareU32(comparisonDst, []testID{0, 2}, 1, simdops.Less)
	if want := []bool{true, false, true}; !slices.Equal(comparisonDst, want) {
		t.Fatalf("CompareU32 shortest source = %v want %v", comparisonDst, want)
	}
	comparisonDst = comparisonDst[:1]
	simdops.CompareU32(comparisonDst, []testID{0, 2}, 1, simdops.Less)
	if !comparisonDst[0] {
		t.Fatalf("CompareU32 shortest destination = %v", comparisonDst)
	}

	wordDst := []uint64{0, 0xfeed}
	simdops.AndWords(wordDst, []uint64{0x0f, 0xff}, []uint64{0x03})
	if want := []uint64{0x03, 0xfeed}; !slices.Equal(wordDst, want) {
		t.Fatalf("AndWords shortest input = %x want %x", wordDst, want)
	}

	maskDst := []bool{false, true}
	simdops.OrMask(maskDst, []bool{true, false}, []bool{false})
	if want := []bool{true, true}; !slices.Equal(maskDst, want) {
		t.Fatalf("OrMask shortest input = %v want %v", maskDst, want)
	}

	compressDst := []testID{99, 99, 99}
	if count := simdops.CompressU32(compressDst, []testID{1, 2, 3}, []bool{true, false}); count != 1 {
		t.Fatalf("CompressU32 shortest mask count = %d want 1", count)
	}
	if want := []testID{1, 99, 99}; !slices.Equal(compressDst, want) {
		t.Fatalf("CompressU32 shortest mask = %v want %v", compressDst, want)
	}
	compressDst = []testID{99, 99, 99}
	if count := simdops.CompressU32(compressDst, []testID{4}, []bool{true, true}); count != 1 {
		t.Fatalf("CompressU32 shortest source count = %d want 1", count)
	}
	if want := []testID{4, 99, 99}; !slices.Equal(compressDst, want) {
		t.Fatalf("CompressU32 shortest source = %v want %v", compressDst, want)
	}

	packDst := []uint64{0xfeedface}
	packWant := slices.Clone(packDst)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("PackMask short destination did not panic")
			}
		}()
		simdops.PackMask(packDst[:0], make([]bool, 1))
	}()
	if !slices.Equal(packDst, packWant) {
		t.Fatalf("PackMask shape failure mutated dst: got %x want %x", packDst, packWant)
	}
}

func testName(n int) string {
	if n == 0 {
		return "n=0"
	}
	buf := [24]byte{'n', '='}
	i := len(buf)
	for value := n; value != 0; value /= 10 {
		i--
		buf[i] = byte('0' + value%10)
	}
	return string(append(buf[:2], buf[i:]...))
}

func testComparisons(t *testing.T, n int, comparisons []simdops.Comparison) {
	t.Helper()
	u32Backing := make([]testID, n+2)
	i64Backing := make([]int64, n+2)
	for i := range n {
		u32Backing[i+1] = testID((i*17 + 3) % 23)
		i64Backing[i+1] = int64((i*29+5)%37) - 18
	}
	u32Values := u32Backing[1 : n+1]
	i64Values := i64Backing[1 : n+1]
	for _, comparison := range comparisons {
		u32Mask := make([]bool, n+2)[1 : n+1]
		i64Mask := make([]bool, n+2)[1 : n+1]
		simdops.CompareU32(u32Mask, u32Values, testID(11), comparison)
		simdops.CompareI64(i64Mask, i64Values, int64(0), comparison)
		for i := range n {
			if got, want := u32Mask[i], compare(uint32(u32Values[i]), 11, comparison); got != want {
				t.Fatalf("n=%d CompareU32(%d) row %d = %v want %v", n, comparison, i, got, want)
			}
			if got, want := i64Mask[i], compare(i64Values[i], 0, comparison); got != want {
				t.Fatalf("n=%d CompareI64(%d) row %d = %v want %v", n, comparison, i, got, want)
			}
		}
	}
}

func compare[T ~uint32 | ~int64](a, b T, op simdops.Comparison) bool {
	switch op {
	case simdops.Equal:
		return a == b
	case simdops.NotEqual:
		return a != b
	case simdops.Less:
		return a < b
	case simdops.LessEqual:
		return a <= b
	case simdops.Greater:
		return a > b
	case simdops.GreaterEqual:
		return a >= b
	}
	panic("invalid test comparison")
}

func testWordOperations(t *testing.T, n int) {
	t.Helper()
	type operation struct {
		name string
		run  func([]uint64, []uint64, []uint64)
		ref  func(uint64, uint64) uint64
	}
	operations := [...]operation{
		{"and", simdops.AndWords, func(a, b uint64) uint64 { return a & b }},
		{"or", simdops.OrWords, func(a, b uint64) uint64 { return a | b }},
		{"xor", simdops.XorWords, func(a, b uint64) uint64 { return a ^ b }},
		{"and-not", simdops.AndNotWords, func(a, b uint64) uint64 { return a &^ b }},
	}
	aBacking := make([]uint64, n+2)
	bBacking := make([]uint64, n+2)
	for i := range n {
		aBacking[i+1] = uint64(i+1)*0x9e3779b97f4a7c15 ^ 0xa5a5a5a5a5a5a5a5
		bBacking[i+1] = uint64(i+3)*0xbf58476d1ce4e5b9 ^ 0x5a5a5a5a5a5a5a5a
	}
	a, b := aBacking[1:n+1], bBacking[1:n+1]
	for _, op := range operations {
		want := make([]uint64, n)
		for i := range n {
			want[i] = op.ref(a[i], b[i])
		}
		got := make([]uint64, n+2)[1 : n+1]
		op.run(got, a, b)
		if !slices.Equal(got, want) {
			t.Fatalf("n=%d %s mismatch", n, op.name)
		}
		alias := slices.Clone(a)
		op.run(alias, alias, b)
		if !slices.Equal(alias, want) {
			t.Fatalf("n=%d %s alias mismatch", n, op.name)
		}
		alias = slices.Clone(b)
		op.run(alias, a, alias)
		if !slices.Equal(alias, want) {
			t.Fatalf("n=%d %s second alias mismatch", n, op.name)
		}
	}
}

func testMaskOperations(t *testing.T, n int) {
	t.Helper()
	aBacking := make([]bool, n+2)
	bBacking := make([]bool, n+2)
	for i := range n {
		aBacking[i+1] = i%2 == 0
		bBacking[i+1] = i%3 == 0
	}
	a, b := aBacking[1:n+1], bBacking[1:n+1]
	type operation struct {
		name string
		run  func([]bool, []bool, []bool)
		ref  func(bool, bool) bool
	}
	operations := [...]operation{
		{"and", simdops.AndMask, func(a, b bool) bool { return a && b }},
		{"or", simdops.OrMask, func(a, b bool) bool { return a || b }},
		{"xor", simdops.XorMask, func(a, b bool) bool { return a != b }},
	}
	for _, op := range operations {
		want := make([]bool, n)
		for i := range n {
			want[i] = op.ref(a[i], b[i])
		}
		got := make([]bool, n+2)[1 : n+1]
		op.run(got, a, b)
		if !slices.Equal(got, want) {
			t.Fatalf("n=%d mask %s mismatch", n, op.name)
		}
		alias := slices.Clone(a)
		op.run(alias, alias, b)
		if !slices.Equal(alias, want) {
			t.Fatalf("n=%d mask %s alias mismatch", n, op.name)
		}
		alias = slices.Clone(b)
		op.run(alias, a, alias)
		if !slices.Equal(alias, want) {
			t.Fatalf("n=%d mask %s second alias mismatch", n, op.name)
		}
	}
	wantNot := make([]bool, n)
	for i := range n {
		wantNot[i] = !a[i]
	}
	gotNot := slices.Clone(a)
	simdops.NotMask(gotNot, gotNot)
	if !slices.Equal(gotNot, wantNot) {
		t.Fatalf("n=%d mask not alias mismatch", n)
	}
}

func testMaskPacking(t *testing.T, n int) {
	t.Helper()
	maskBacking := make([]bool, n+2)
	for i := range n {
		maskBacking[i+1] = i%3 == 0 || i%7 == 2
	}
	mask := maskBacking[1 : n+1]
	words := (n + 63) / 64
	backing := make([]uint64, words+2)
	for i := range backing {
		backing[i] = ^uint64(0)
	}
	got := backing[1 : words+1]
	simdops.PackMask(got, mask)
	want := make([]uint64, words)
	for row, set := range mask {
		if set {
			want[row>>6] |= uint64(1) << (uint(row) & 63)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("n=%d PackMask = %x want %x", n, got, want)
	}
}

func testCompression(t *testing.T, n int) {
	t.Helper()
	backing := make([]testID, n+2)
	maskBacking := make([]bool, n+2)
	for i := range n {
		backing[i+1] = testID(i*7 + 1)
		maskBacking[i+1] = i%3 != 1
	}
	src, mask := backing[1:n+1], maskBacking[1:n+1]
	want := make([]testID, 0, n)
	for i, keep := range mask {
		if keep {
			want = append(want, src[i])
		}
	}
	for _, capacity := range [...]int{len(want), len(want) / 2} {
		dst := make([]testID, capacity+2)[1 : capacity+1]
		count := simdops.CompressU32(dst, src, mask)
		if count != capacity || !slices.Equal(dst[:count], want[:capacity]) {
			t.Fatalf("n=%d CompressU32 cap=%d = %d %v want %v", n, capacity, count, dst[:count], want[:capacity])
		}
	}
}
