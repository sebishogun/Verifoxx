package schema

import (
	"bytes"
	"math/bits"

	"github.com/sebishogun/nornrune/internal/arena"
)

// FNV-1a 64-bit offset and prime.
const (
	fnvOffset = uint64(14695981039346656037)
	fnvPrime  = uint64(1099511628211)
)

// HashSymbol hashes symbol bytes with FNV-1a 64-bit directly; it never
// allocates or converts to a string. It is the one shared hash contract for
// symbol interning, frozen Program symbol lookup, and batch extension
// interning, so all three stay byte-identical by construction.
func HashSymbol(b []byte) uint64 {
	h := fnvOffset
	for _, c := range b {
		h ^= uint64(c)
		h *= fnvPrime
	}
	return h
}

// nextPow2 returns the smallest power of two >= n (n >= 1).
func nextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	return int(uint(1) << bits.Len(uint(n-1)))
}

// Interner interns byte slices into SymbolIDs using open addressing over
// pre-sized parallel arrays. Bytes are hashed directly (no string
// conversion) and stored once in an internal byte arena; duplicate intern
// returns the existing SymbolID without appending bytes. SymbolID zero is
// invalid; IDs are assigned sequentially starting at 1 and reset restores
// deterministic assignment while retaining capacity. An Interner is not safe
// for concurrent use by multiple goroutines.
type Interner struct {
	arena   *arena.ByteArena
	ids     []SymbolID // per open-addressing slot; 0 = empty
	refs    []arena.Ref
	starts  []uint32 // indexed by SymbolID-1
	lengths []uint32 // indexed by SymbolID-1
	count   int
	nextID  SymbolID
}

// NewSymbolInterner returns an interner pre-sized for roughly hint symbols.
func NewSymbolInterner(hint int) *Interner {
	if hint < 0 {
		hint = 0
	}
	size := nextPow2(2 * hint)
	if size < 4 {
		size = 4
	}
	return &Interner{
		arena:   arena.New(hint),
		ids:     make([]SymbolID, size),
		refs:    make([]arena.Ref, size),
		starts:  make([]uint32, 0, hint),
		lengths: make([]uint32, 0, hint),
		nextID:  1,
	}
}

// ensureArena lazily allocates the byte arena so the zero-value Interner is
// usable.
func (in *Interner) ensureArena() {
	if in.arena == nil {
		in.arena = arena.New(0)
	}
}

// Len returns the number of distinct symbols interned.
func (in *Interner) Len() int { return in.count }

// ByteLen returns the number of bytes committed to the internal arena, or 0
// before the first Intern call initializes the arena.
func (in *Interner) ByteLen() uint32 {
	if in.arena == nil {
		return 0
	}
	return in.arena.Len()
}

// Intern returns the SymbolID for b, interning it on first sight. It returns
// an error only when the internal byte arena overflows; the interner is left
// unchanged in that case.
func (in *Interner) Intern(b []byte) (SymbolID, error) {
	if len(in.ids) == 0 {
		in.initTable(4)
	}
	in.ensureArena()
	for {
		mask := uint64(len(in.ids) - 1)
		slot := int(HashSymbol(b) & mask)
		for {
			id := in.ids[slot]
			if id == 0 {
				break
			}
			if bytes.Equal(in.arena.Bytes(in.refs[slot]), b) {
				return id, nil
			}
			slot = (slot + 1) & int(mask)
		}
		if in.count*2 < len(in.ids) {
			ref, err := in.arena.Append(b)
			if err != nil {
				return 0, err
			}
			id := in.nextID
			in.nextID++
			in.ids[slot] = id
			in.refs[slot] = ref
			in.starts = append(in.starts, ref.Offset)
			in.lengths = append(in.lengths, ref.Length)
			in.count++
			return id, nil
		}
		in.grow()
	}
}

// Lookup returns the SymbolID for b if it was already interned, without
// interning. It performs no allocation.
func (in *Interner) Lookup(b []byte) (SymbolID, bool) {
	if len(in.ids) == 0 {
		return 0, false
	}
	mask := uint64(len(in.ids) - 1)
	slot := int(HashSymbol(b) & mask)
	for {
		id := in.ids[slot]
		if id == 0 {
			return 0, false
		}
		if bytes.Equal(in.arena.Bytes(in.refs[slot]), b) {
			return id, true
		}
		slot = (slot + 1) & int(mask)
	}
}

// Bytes returns the interned bytes for id, or ok=false for the invalid zero
// ID or any out-of-range ID. The returned slice is a read-only view into the
// interner's arena; do not retain it across later Intern calls.
func (in *Interner) Bytes(id SymbolID) ([]byte, bool) {
	i := int(id)
	if i <= 0 || i > len(in.starts) || in.arena == nil {
		return nil, false
	}
	b := in.arena.Bytes(arena.Ref{Offset: in.starts[i-1], Length: in.lengths[i-1]})
	if b == nil {
		return nil, false
	}
	return b, true
}

// Reset clears all interned symbols and restores sequential ID assignment
// starting at 1, while retaining table and arena capacity.
func (in *Interner) Reset() {
	for i := range in.ids {
		in.ids[i] = 0
	}
	in.starts = in.starts[:0]
	in.lengths = in.lengths[:0]
	in.count = 0
	in.nextID = 1
	if in.arena != nil {
		in.arena.Reset()
	}
}

func (in *Interner) initTable(size int) {
	in.ids = make([]SymbolID, size)
	in.refs = make([]arena.Ref, size)
	in.nextID = 1
}

func (in *Interner) grow() {
	newSize := len(in.ids) * 2
	oldIDs := in.ids
	oldRefs := in.refs
	in.ids = make([]SymbolID, newSize)
	in.refs = make([]arena.Ref, newSize)
	mask := uint64(newSize - 1)
	for i, id := range oldIDs {
		if id == 0 {
			continue
		}
		b := in.arena.Bytes(oldRefs[i])
		slot := int(HashSymbol(b) & mask)
		for in.ids[slot] != 0 {
			slot = (slot + 1) & int(mask)
		}
		in.ids[slot] = id
		in.refs[slot] = oldRefs[i]
	}
}
