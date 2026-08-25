package truth

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

func TestWordCount(t *testing.T) {
	tests := []struct {
		rows uint32
		want int
	}{
		{0, 0},
		{1, 1},
		{63, 1},
		{64, 1},
		{65, 2},
		{127, 2},
		{128, 2},
		{129, 3},
		{math.MaxUint32, 67_108_864},
	}
	for _, tc := range tests {
		if got := WordCount(tc.rows); got != tc.want {
			t.Errorf("WordCount(%d) = %d, want %d", tc.rows, got, tc.want)
		}
	}
}

// Test states are encoded one word per state: low bit positive, high bit
// negative. Logical order: unknown, true, false, conflict.
const (
	stUnknown  uint64 = 0b00
	stTrue     uint64 = 0b01
	stFalse    uint64 = 0b10
	stConflict uint64 = 0b11
)

var stateNames = [4]string{"unknown", "true", "false", "conflict"}

var states = [4]uint64{stUnknown, stTrue, stFalse, stConflict}

// Literal truth tables, indexed row=left operand, column=right operand, both
// in logical order unknown, true, false, conflict.
var notTable = [4]uint64{stUnknown, stFalse, stTrue, stConflict}

var andTable = [4][4]uint64{
	{stUnknown, stUnknown, stFalse, stFalse},
	{stUnknown, stTrue, stFalse, stConflict},
	{stFalse, stFalse, stFalse, stFalse},
	{stFalse, stConflict, stFalse, stConflict},
}

var orTable = [4][4]uint64{
	{stUnknown, stTrue, stUnknown, stTrue},
	{stTrue, stTrue, stTrue, stTrue},
	{stUnknown, stTrue, stFalse, stConflict},
	{stTrue, stTrue, stConflict, stConflict},
}

// packStates packs one test state per row into a one-word bitplane pair.
func packStates(states []uint64) Planes {
	var pos, neg uint64
	for i, s := range states {
		pos |= s & 1 << uint(i)
		neg |= s >> 1 & 1 << uint(i)
	}
	return Planes{Positive: []uint64{pos}, Negative: []uint64{neg}}
}

// pairOperands packs the 16 ordered pairs (l, r) into row l*4+r of two
// one-word bitplanes, so both operands of every pair share their row index.
func pairOperands() (left, right Planes) {
	var lpos, lneg, rpos, rneg uint64
	for i := 0; i < 16; i++ {
		l, r := states[i/4], states[i%4]
		lpos |= l & 1 << uint(i)
		lneg |= l >> 1 & 1 << uint(i)
		rpos |= r & 1 << uint(i)
		rneg |= r >> 1 & 1 << uint(i)
	}
	return Planes{Positive: []uint64{lpos}, Negative: []uint64{lneg}},
		Planes{Positive: []uint64{rpos}, Negative: []uint64{rneg}}
}

// rowState decodes bit 0 of row i as positive, bit 1 as negative.
func rowState(p *Planes, i int) uint64 {
	pos := p.Positive[0] >> uint(i) & 1
	neg := p.Negative[0] >> uint(i) & 1
	return pos | neg<<1
}

func TestTruthTables(t *testing.T) {
	t.Run("Not", func(t *testing.T) {
		src := packStates([]uint64{stUnknown, stTrue, stFalse, stConflict})
		var dst Planes
		dst.Positive = make([]uint64, 1)
		dst.Negative = make([]uint64, 1)
		Not(dst, src, 4)
		for i, want := range notTable {
			t.Run("Not/"+stateNames[i], func(t *testing.T) {
				if got := rowState(&dst, i); got != want {
					t.Errorf("Not(%s) = %s, want %s", stateNames[i], stateNames[got], stateNames[want])
				}
			})
		}
	})

	t.Run("And", func(t *testing.T) {
		left, right := pairOperands()
		var dst Planes
		dst.Positive = make([]uint64, 1)
		dst.Negative = make([]uint64, 1)
		And(dst, left, right, 16)
		for l, lname := range stateNames {
			for r, rname := range stateNames {
				t.Run("And/"+lname+"/"+rname, func(t *testing.T) {
					row := l*4 + r
					want := andTable[l][r]
					if got := rowState(&dst, row); got != want {
						t.Errorf("And(%s, %s) = %s, want %s", lname, rname, stateNames[got], stateNames[want])
					}
				})
			}
		}
	})

	t.Run("Or", func(t *testing.T) {
		left, right := pairOperands()
		var dst Planes
		dst.Positive = make([]uint64, 1)
		dst.Negative = make([]uint64, 1)
		Or(dst, left, right, 16)
		for l, lname := range stateNames {
			for r, rname := range stateNames {
				t.Run("Or/"+lname+"/"+rname, func(t *testing.T) {
					row := l*4 + r
					want := orTable[l][r]
					if got := rowState(&dst, row); got != want {
						t.Errorf("Or(%s, %s) = %s, want %s", lname, rname, stateNames[got], stateNames[want])
					}
				})
			}
		}
	})
}

// TestOperationsSupportExactAliasing checks that dst may exactly alias src,
// left, or right (whole-plane aliasing) for every operation. Expected results
// are computed out of place first, then each aliased call must reproduce them
// bit-for-bit in both planes, clean tail included. Partial overlaps and
// positive/negative cross-plane aliasing are outside the contract.
func TestOperationsSupportExactAliasing(t *testing.T) {
	const rows = uint32(129)
	left := buildPlanes(rows, func(i uint32) uint64 { return testState(int(i & 3)) })
	right := buildPlanes(rows, func(i uint32) uint64 { return testState(int(i*5 + 2)) })
	lPos, lNeg := slices.Clone(left.Positive), slices.Clone(left.Negative)
	rPos, rNeg := slices.Clone(right.Positive), slices.Clone(right.Negative)

	notWant := buildPlanes(rows, func(uint32) uint64 { return 0 })
	Not(notWant, left, rows)
	andWant := buildPlanes(rows, func(uint32) uint64 { return 0 })
	And(andWant, left, right, rows)
	orWant := buildPlanes(rows, func(uint32) uint64 { return 0 })
	Or(orWant, left, right, rows)

	if !slices.Equal(left.Positive, lPos) || !slices.Equal(left.Negative, lNeg) {
		t.Error("out-of-place calls mutated left source")
	}
	if !slices.Equal(right.Positive, rPos) || !slices.Equal(right.Negative, rNeg) {
		t.Error("out-of-place calls mutated right source")
	}

	clone := func(p Planes) Planes {
		return Planes{Positive: slices.Clone(p.Positive), Negative: slices.Clone(p.Negative)}
	}
	assertPlanes := func(t *testing.T, name string, got, want Planes) {
		t.Helper()
		if !slices.Equal(got.Positive, want.Positive) {
			t.Errorf("%s positive plane = %#x, want %#x", name, got.Positive, want.Positive)
		}
		if !slices.Equal(got.Negative, want.Negative) {
			t.Errorf("%s negative plane = %#x, want %#x", name, got.Negative, want.Negative)
		}
	}

	t.Run("Not dst=src", func(t *testing.T) {
		alias := clone(left)
		Not(alias, alias, rows)
		assertPlanes(t, "Not(alias, alias)", alias, notWant)
	})
	t.Run("And dst=left", func(t *testing.T) {
		alias := clone(left)
		And(alias, alias, right, rows)
		assertPlanes(t, "And(alias, alias, right)", alias, andWant)
	})
	t.Run("And dst=right", func(t *testing.T) {
		alias := clone(right)
		And(alias, left, alias, rows)
		assertPlanes(t, "And(alias, left, alias)", alias, andWant)
	})
	t.Run("Or dst=left", func(t *testing.T) {
		alias := clone(left)
		Or(alias, alias, right, rows)
		assertPlanes(t, "Or(alias, alias, right)", alias, orWant)
	})
	t.Run("Or dst=right", func(t *testing.T) {
		alias := clone(right)
		Or(alias, left, alias, rows)
		assertPlanes(t, "Or(alias, left, alias)", alias, orWant)
	})
}

// testState cycles a row index through the four test states.
func testState(i int) uint64 { return states[i&3] }

// buildPlanes packs one state per row into exact-sized bitplanes. For a
// partial final word the unused bits are poisoned to 1 in both planes so
// dirty-tail leakage is observable; zero rows yields nil planes.
func buildPlanes(rows uint32, stateFor func(i uint32) uint64) Planes {
	words := WordCount(rows)
	if words == 0 {
		return Planes{}
	}
	pos := make([]uint64, words)
	neg := make([]uint64, words)
	for i := uint32(0); i < rows; i++ {
		s := stateFor(i)
		pos[i>>6] |= s & 1 << uint(i&63)
		neg[i>>6] |= s >> 1 << uint(i&63)
	}
	if rem := rows & 63; rem != 0 {
		poison := ^((uint64(1) << rem) - 1)
		pos[words-1] |= poison
		neg[words-1] |= poison
	}
	return Planes{Positive: pos, Negative: neg}
}

// rowStateAt decodes the two-bit state of logical row i across the planes.
func rowStateAt(p *Planes, i uint32) uint64 {
	pos := p.Positive[i>>6] >> uint(i&63) & 1
	neg := p.Negative[i>>6] >> uint(i&63) & 1
	return pos | neg<<1
}

func TestOperationsClearTailBits(t *testing.T) {
	rowsCases := []uint32{0, 1, 63, 64, 65, 127, 128, 129}
	ops := []struct {
		name   string
		run    func(dst, left, right Planes, rows uint32)
		expect func(l, r uint64) uint64
	}{
		{"Not", func(dst, left, right Planes, rows uint32) { Not(dst, left, rows) },
			func(l, r uint64) uint64 { return notTable[l] }},
		{"And", And, func(l, r uint64) uint64 { return andTable[l][r] }},
		{"Or", Or, func(l, r uint64) uint64 { return orTable[l][r] }},
	}
	for _, op := range ops {
		for _, rows := range rowsCases {
			t.Run(fmt.Sprintf("%s/rows=%d", op.name, rows), func(t *testing.T) {
				left := buildPlanes(rows, func(i uint32) uint64 { return testState(int(i & 3)) })
				right := buildPlanes(rows, func(i uint32) uint64 { return testState(int(i*3 + 1)) })
				dst := buildPlanes(rows, func(uint32) uint64 { return 0 })
				lPos, lNeg := slices.Clone(left.Positive), slices.Clone(left.Negative)
				rPos, rNeg := slices.Clone(right.Positive), slices.Clone(right.Negative)

				op.run(dst, left, right, rows)

				if !slices.Equal(left.Positive, lPos) || !slices.Equal(left.Negative, lNeg) {
					t.Error("left operand mutated")
				}
				if !slices.Equal(right.Positive, rPos) || !slices.Equal(right.Negative, rNeg) {
					t.Error("right operand mutated")
				}
				for i := uint32(0); i < rows; i++ {
					got := rowStateAt(&dst, i)
					want := op.expect(testState(int(i&3)), testState(int(i*3+1)))
					if got != want {
						t.Errorf("row %d: state = %s, want %s", i, stateNames[got], stateNames[want])
					}
				}
				if words := len(dst.Positive); words > 0 {
					if rem := rows & 63; rem != 0 {
						mask := (uint64(1) << rem) - 1
						if tail := dst.Positive[words-1] & ^mask; tail != 0 {
							t.Errorf("positive tail bits %#x nonzero", tail)
						}
						if tail := dst.Negative[words-1] & ^mask; tail != 0 {
							t.Errorf("negative tail bits %#x nonzero", tail)
						}
					}
				}
			})
		}
	}
}

func TestSetWritesExactBooleanAndClearsDirtyTail(t *testing.T) {
	for _, rows := range []uint32{0, 1, 63, 64, 65, 1024} {
		for _, value := range []bool{false, true} {
			t.Run(fmt.Sprintf("rows=%d/value=%t", rows, value), func(t *testing.T) {
				words := WordCount(rows)
				dst := Planes{Positive: make([]uint64, words), Negative: make([]uint64, words)}
				for word := range words {
					dst.Positive[word] = math.MaxUint64
					dst.Negative[word] = math.MaxUint64
				}
				Set(dst, value, rows)
				for row := uint32(0); row < rows; row++ {
					want := stFalse
					if value {
						want = stTrue
					}
					if got := rowStateAt(&dst, row); got != want {
						t.Fatalf("row %d = %s, want %s", row, stateNames[got], stateNames[want])
					}
				}
				if words != 0 && rows&63 != 0 {
					mask := uint64(1)<<(rows&63) - 1
					if dst.Positive[words-1]&^mask != 0 || dst.Negative[words-1]&^mask != 0 {
						t.Fatalf("dirty tail remains: positive=%#x negative=%#x", dst.Positive, dst.Negative)
					}
				}
			})
		}
	}
}

func TestSetRejectsMalformedShapeBeforeWrite(t *testing.T) {
	positive := []uint64{11, 12, 13}
	negative := []uint64{21, 22, 23}
	wantPositive := slices.Clone(positive)
	wantNegative := slices.Clone(negative)
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		Set(Planes{Positive: positive[:1], Negative: negative[:2]}, true, 65)
	}()
	if !panicked {
		t.Fatal("Set accepted malformed shape")
	}
	if !slices.Equal(positive, wantPositive) || !slices.Equal(negative, wantNegative) {
		t.Fatalf("Set wrote before shape validation: positive=%v negative=%v", positive, negative)
	}
}

// TestOperationsRejectMalformedShapesBeforeWrite drives every plane of every
// operand of every operation with one malformed length at a time: one word
// short and one word long, at rows=65 where the valid length is two words.
// Every plane is backed by a three-word array filled with per-word canaries
// and the full backing (extra capacity word included) is snapshotted before
// the call. Each subtest asserts the operation panics and leaves both
// destination backing arrays untouched, which catches silent acceptance of
// long slices and partial destination writes that precede a bounds panic on
// short slices.
func TestOperationsRejectMalformedShapesBeforeWrite(t *testing.T) {
	const rows = uint32(65)
	const words = 2 // WordCount(rows): 65 rows need two words
	const capWords = words + 1

	// canary yields a distinct sentinel for each (operand, plane, word).
	canary := func(operand, plane, word int) uint64 {
		v := uint64(operand*6 + plane*3 + word + 1)
		return v<<32 | v
	}

	// buildOperands returns three freshly canaried operand planes with every
	// plane at the exact [:words] length except plane selPlane of operand sel,
	// which is resliced to selLen, plus the full destination backing arrays.
	buildOperands := func(sel, selPlane, selLen int) ([]Planes, []uint64, []uint64) {
		operands := make([]Planes, 3)
		var dstPos, dstNeg []uint64
		for i := 0; i < 3; i++ {
			pos := make([]uint64, capWords)
			neg := make([]uint64, capWords)
			for w := 0; w < capWords; w++ {
				pos[w] = canary(i, 0, w)
				neg[w] = canary(i, 1, w)
			}
			posLen, negLen := words, words
			if i == sel {
				if selPlane == 0 {
					posLen = selLen
				} else {
					negLen = selLen
				}
			}
			operands[i] = Planes{Positive: pos[:posLen], Negative: neg[:negLen]}
			if i == 0 {
				dstPos, dstNeg = pos, neg
			}
		}
		return operands, dstPos, dstNeg
	}

	type operation struct {
		name  string
		arity int
		names [3]string
		run   func([]Planes, uint32)
	}
	operations := []operation{
		{"Not", 2, [3]string{"dst", "src", ""}, func(p []Planes, rows uint32) { Not(p[0], p[1], rows) }},
		{"And", 3, [3]string{"dst", "left", "right"}, func(p []Planes, rows uint32) { And(p[0], p[1], p[2], rows) }},
		{"Or", 3, [3]string{"dst", "left", "right"}, func(p []Planes, rows uint32) { Or(p[0], p[1], p[2], rows) }},
	}
	planeNames := []string{"Positive", "Negative"}
	deltas := []struct {
		name   string
		length int
	}{{"short", words - 1}, {"long", words + 1}}

	for _, op := range operations {
		for sel := 0; sel < op.arity; sel++ {
			for _, plane := range planeNames {
				for _, delta := range deltas {
					selPlane := 0
					if plane == "Negative" {
						selPlane = 1
					}
					t.Run(fmt.Sprintf("%s/%s/%s/%s", op.name, op.names[sel], plane, delta.name), func(t *testing.T) {
						operands, dstPos, dstNeg := buildOperands(sel, selPlane, delta.length)
						posWant, negWant := slices.Clone(dstPos), slices.Clone(dstNeg)

						panicked := false
						func() {
							defer func() { panicked = recover() != nil }()
							op.run(operands, rows)
						}()

						if !panicked {
							t.Error("operation accepted malformed plane shape: expected panic")
						}
						if !slices.Equal(dstPos, posWant) {
							t.Errorf("destination positive backing modified: got %#x, want %#x", dstPos, posWant)
						}
						if !slices.Equal(dstNeg, negWant) {
							t.Errorf("destination negative backing modified: got %#x, want %#x", dstNeg, negWant)
						}
					})
				}
			}
		}
	}
}
