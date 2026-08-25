package truth

// Planes is a non-owning batch view over two uint64 bitplanes: one for
// positive rows and one for negative rows. It does not own the underlying
// storage; callers must keep the slices alive for the view's lifetime.
//
// Every plane passed to an operation must have exactly WordCount(rows) words.
// Within a single writable value the Positive and Negative slices must be
// distinct, and any two planes that share storage must do so exactly: the
// operations support whole-value aliasing (a destination that is the same
// value as a source) only. Shifted partial overlaps and cross-plane aliases
// (a destination plane sharing a source's other plane) are unsupported.
type Planes struct {
	Positive []uint64
	Negative []uint64
}

// WordCount returns the number of 64-bit words needed to store rows values as
// a bitplane. It is the ceiling of rows/64 and does not overflow for
// math.MaxUint32.
func WordCount(rows uint32) int {
	return int((uint64(rows) + 63) >> 6)
}

// requireShape panics if either plane does not hold exactly words entries.
// Operations validate every operand before writing any destination
// word, so a malformed plane cannot be silently accepted or partially
// written; rows=0 with nil or empty planes remains valid.
func requireShape(planes Planes, words int) {
	if len(planes.Positive) != words || len(planes.Negative) != words {
		panic("truth: plane length does not match row count")
	}
}

// Set writes the same exact Boolean value to every logical row.
func Set(dst Planes, value bool, rows uint32) {
	words := WordCount(rows)
	requireShape(dst, words)
	positive, negative := uint64(0), ^uint64(0)
	if value {
		positive, negative = negative, positive
	}
	for word := 0; word < words; word++ {
		dst.Positive[word] = positive
		dst.Negative[word] = negative
	}
	maskTail(dst, rows)
}

// Not writes the negation of src into dst: the positive output plane is the
// source's negative plane and vice versa, so each row's state flips polarity.
// dst may exactly alias src.
func Not(dst, src Planes, rows uint32) {
	words := WordCount(rows)
	requireShape(dst, words)
	requireShape(src, words)
	dstPositive := dst.Positive[:words:words]
	dstNegative := dst.Negative[:words:words]
	srcPositive := src.Positive[:words:words]
	srcNegative := src.Negative[:words:words]
	for i := 0; i < words; i++ {
		positive, negative := notWord(srcPositive[i], srcNegative[i])
		dstPositive[i] = positive
		dstNegative[i] = negative
	}
	maskTail(dst, rows)
}

// And writes the conjunction of left and right into dst: a row is positive
// only where both inputs are positive, and negative wherever either input is.
// dst may exactly alias left or right.
func And(dst, left, right Planes, rows uint32) {
	words := WordCount(rows)
	requireShape(dst, words)
	requireShape(left, words)
	requireShape(right, words)
	dstPositive := dst.Positive[:words:words]
	dstNegative := dst.Negative[:words:words]
	leftPositive := left.Positive[:words:words]
	leftNegative := left.Negative[:words:words]
	rightPositive := right.Positive[:words:words]
	rightNegative := right.Negative[:words:words]
	for i := 0; i < words; i++ {
		positive, negative := andWord(leftPositive[i], leftNegative[i], rightPositive[i], rightNegative[i])
		dstPositive[i] = positive
		dstNegative[i] = negative
	}
	maskTail(dst, rows)
}

// Or writes the disjunction of left and right into dst: a row is positive
// wherever either input is, and negative only where both inputs are.
// dst may exactly alias left or right.
func Or(dst, left, right Planes, rows uint32) {
	words := WordCount(rows)
	requireShape(dst, words)
	requireShape(left, words)
	requireShape(right, words)
	dstPositive := dst.Positive[:words:words]
	dstNegative := dst.Negative[:words:words]
	leftPositive := left.Positive[:words:words]
	leftNegative := left.Negative[:words:words]
	rightPositive := right.Positive[:words:words]
	rightNegative := right.Negative[:words:words]
	for i := 0; i < words; i++ {
		positive, negative := orWord(leftPositive[i], leftNegative[i], rightPositive[i], rightNegative[i])
		dstPositive[i] = positive
		dstNegative[i] = negative
	}
	maskTail(dst, rows)
}

// maskTail zeroes the unused bits of the final word in both destination
// planes: rows need not be a multiple of 64, and a dirty tail in the source
// must never leak into the batch output.
func maskTail(dst Planes, rows uint32) {
	remaining := rows & 63
	if remaining == 0 {
		return
	}
	last := len(dst.Positive) - 1
	mask := (uint64(1) << remaining) - 1
	dst.Positive[last] &= mask
	dst.Negative[last] &= mask
}
