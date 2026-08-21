// Package arena provides a contiguous byte arena with fixed-width
// offset/length references and an internal byte limit for overflow checking.
//
// References are offsets into one growable backing array, so committed bytes
// remain reachable across reallocations until Reset. Slices returned by Bytes
// are views into the arena and must be treated as read-only; do not retain
// them across later Appends.
package arena

import "errors"

// ErrTooLarge reports an append that would exceed the arena's byte limit.
var ErrTooLarge = errors.New("arena: append exceeds capacity limit")

const maxUint32 = uint32(^uint32(0))

// Ref is a fixed-width reference into a ByteArena.
type Ref struct {
	Offset uint32
	Length uint32
}

// ByteArena stores bytes contiguously. The zero value is an empty arena with
// the default byte limit of 2^32-1. A ByteArena is not safe for concurrent
// use by multiple goroutines.
type ByteArena struct {
	buf      []byte
	maxBytes uint32
}

// limit returns the effective byte limit: maxBytes when set, otherwise the
// full uint32 address space. A zero maxBytes therefore means "no explicit
// limit" for the zero value, while a package-private finite maxBytes still
// bounds Appends and Grow calls.
func (a *ByteArena) limit() uint32 {
	if a.maxBytes == 0 {
		return maxUint32
	}
	return a.maxBytes
}

// New returns an empty arena with at least hint bytes of capacity and the
// default byte limit of 2^32-1.
func New(hint int) *ByteArena {
	if hint < 0 {
		hint = 0
	}
	return &ByteArena{buf: make([]byte, 0, hint), maxBytes: maxUint32}
}

// Append copies p into the arena and returns a fixed-width reference. It
// returns ErrTooLarge without modifying the arena when the append would
// exceed the byte limit. An empty p is recorded without allocating.
func (a *ByteArena) Append(p []byte) (Ref, error) {
	off := uint32(len(a.buf))
	if len(p) == 0 {
		if a.buf == nil {
			a.buf = make([]byte, 0)
		}
		return Ref{Offset: off, Length: 0}, nil
	}
	if uint64(len(a.buf))+uint64(len(p)) > uint64(a.limit()) {
		return Ref{}, ErrTooLarge
	}
	a.buf = append(a.buf, p...)
	return Ref{Offset: off, Length: uint32(len(p))}, nil
}

// Bytes returns the committed bytes for r, or nil when r is out of bounds or
// its range overflows the arena. The returned slice is a read-only view.
func (a *ByteArena) Bytes(r Ref) []byte {
	end := uint64(r.Offset) + uint64(r.Length)
	if end > uint64(len(a.buf)) {
		return nil
	}
	return a.buf[int(r.Offset) : int(r.Offset)+int(r.Length)]
}

// Len returns the number of committed bytes.
func (a *ByteArena) Len() uint32 { return uint32(len(a.buf)) }

// Cap returns the current capacity of the backing array.
func (a *ByteArena) Cap() int { return cap(a.buf) }

// Grow ensures at least hint total capacity, retaining existing bytes.
func (a *ByteArena) Grow(hint int) {
	if hint <= cap(a.buf) {
		return
	}
	if uint64(hint) > uint64(a.limit()) {
		hint = int(a.limit())
	}
	nb := make([]byte, len(a.buf), hint)
	copy(nb, a.buf)
	a.buf = nb
}

// Reset discards all committed bytes while retaining capacity.
func (a *ByteArena) Reset() {
	a.buf = a.buf[:0]
}
