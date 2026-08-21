package compile

import (
	"bytes"
	"math"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// resizeSlots sizes dst to n elements, reusing its capacity when sufficient,
// and clears the active range so stale open-address entries and remap rows
// from a previous document never leak into the next lowering.
func resizeSlots[T any](dst []T, n int) []T {
	if cap(dst) < n {
		return make([]T, n)
	}
	dst = dst[:n]
	clear(dst)
	return dst
}

// slotSize returns the smallest power of two at least 2n with a floor of 4,
// so an open-address table over n entries never exceeds 50% occupancy. It
// mirrors the schema.Interner sizing rule. A zero return reports that 2n
// exceeds the platform's int range.
func slotSize(n int) int {
	size := 4
	for uint64(size) < 2*uint64(n) {
		if size > math.MaxInt/2 {
			return 0
		}
		size *= 2
	}
	return size
}

// addCounts sums two element counts with widened arithmetic, rejecting a sum
// that cannot be doubled into a platform-sized slot table or addressed by a
// uint32 program column.
func addCounts(a, b int) (int, error) {
	sum := uint64(a) + uint64(b)
	if sum >= uint64(math.MaxUint32) || sum > uint64(math.MaxInt32)/4 {
		return 0, ErrProgramTooLarge
	}
	return int(sum), nil
}

// valueHash folds the canonical kind and payload into the open-address hash
// for the canonical value table. Symbol payloads reuse the shared
// schema.HashSymbol contract so symbol interning, value interning, and frozen
// Program lookup stay aligned; typed payloads fold the kind byte and the
// fixed-width payload word, so equal payloads of different kinds (integer 1,
// Boolean true, timestamp 1) never share a hash by construction.
func valueHash(kind schema.ValueKind, symbol []byte, integer, timestamp int64, boolean uint8) uint64 {
	var payload uint64
	switch kind {
	case schema.ValueKindSymbol:
		payload = schema.HashSymbol(symbol)
	case schema.ValueKindInteger:
		payload = uint64(integer)
	case schema.ValueKindBoolean:
		payload = uint64(boolean)
	case schema.ValueKindTimestamp:
		payload = uint64(timestamp)
	}
	return mixKindPayload(uint64(kind), payload)
}

// mixKindPayload avalanche-mixes the kind byte and the payload word so the
// masked slot depends on both. Collisions still resolve by exact comparison.
func mixKindPayload(kind, payload uint64) uint64 {
	h := kind*0x9E3779B97F4A7C15 ^ payload
	h ^= h >> 30
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 27
	h *= 0x94D049BB133111EB
	return h ^ (h >> 31)
}

// internSymbol returns the canonical Program SymbolID for b, appending the
// bytes to the destination symbol slab when first seen. Exact comparison
// reads existing bytes back from the destination slab through Program.Symbol,
// so no per-symbol storage is retained in the Lowerer.
func (l *Lowerer) internSymbol(dst *program.Program, b []byte) (schema.SymbolID, error) {
	if len(l.symIDs) == 0 {
		return 0, ErrProgramTooLarge
	}
	mask := uint64(len(l.symIDs) - 1)
	h := schema.HashSymbol(b)
	slot := int(h & mask)
	probes := 0
	for ; probes < len(l.symIDs); probes++ {
		id := l.symIDs[slot]
		if id == 0 {
			break
		}
		existing, ok := dst.Symbol(id)
		if !ok {
			return 0, ErrInvalidDocument
		}
		if bytes.Equal(existing, b) {
			return id, nil
		}
		slot = (slot + 1) & int(mask)
	}
	if probes >= len(l.symIDs) {
		return 0, ErrProgramTooLarge
	}
	if uint64(len(dst.SymbolStarts)) >= uint64(math.MaxUint32) {
		return 0, ErrProgramTooLarge
	}
	if uint64(len(dst.SymbolBytes))+uint64(len(b)) > uint64(math.MaxUint32) {
		return 0, ErrProgramTooLarge
	}
	start := uint32(len(dst.SymbolBytes))
	id := schema.SymbolID(len(dst.SymbolStarts) + 1)
	dst.SymbolBytes = append(dst.SymbolBytes, b...)
	dst.SymbolStarts = append(dst.SymbolStarts, start)
	dst.SymbolLengths = append(dst.SymbolLengths, uint32(len(b)))
	l.symHashes[slot] = h
	l.symIDs[slot] = id
	return id, nil
}

// internValue returns the canonical Program ValueID for the candidate
// (kind, payload), appending the payload to the matching packed destination
// column on first sight. Payload references are one-based for every kind: a
// symbol ref is the canonical SymbolID, and integer, Boolean, and timestamp
// refs index their packed columns at ref-1. Exact collision comparison reads
// the canonical payload back from the destination columns.
func (l *Lowerer) internValue(dst *program.Program, h uint64, kind schema.ValueKind, symbol schema.SymbolID, integer, timestamp int64, boolean uint8) (schema.ValueID, error) {
	if len(l.valIDs) == 0 {
		return 0, ErrProgramTooLarge
	}
	mask := uint64(len(l.valIDs) - 1)
	slot := int(h & mask)
	probes := 0
	for ; probes < len(l.valIDs); probes++ {
		id := l.valIDs[slot]
		if id == 0 {
			break
		}
		if valueEqual(dst, l.valKinds[slot], l.valRefs[slot], kind, symbol, integer, timestamp, boolean) {
			return id, nil
		}
		slot = (slot + 1) & int(mask)
	}
	if probes >= len(l.valIDs) {
		return 0, ErrProgramTooLarge
	}
	if uint64(len(dst.ValueKinds)) >= uint64(math.MaxUint32) {
		return 0, ErrProgramTooLarge
	}
	ref, err := appendValuePayload(dst, kind, symbol, integer, timestamp, boolean)
	if err != nil {
		return 0, err
	}
	id := schema.ValueID(len(dst.ValueKinds) + 1)
	dst.ValueKinds = append(dst.ValueKinds, kind)
	dst.ValueRefs = append(dst.ValueRefs, ref)
	l.valHashes[slot] = h
	l.valKinds[slot] = kind
	l.valRefs[slot] = ref
	l.valIDs[slot] = id
	return id, nil
}

// valueEqual reports whether the candidate payload equals the canonical value
// stored in the slot (stored, ref) of dst's packed payload columns. The
// candidate kind must equal the stored kind before any payload comparison:
// the payload word 0 is shared by Integer 0, Boolean false, and Timestamp 0,
// and zero is also the payload parameter of every kind a candidate does not
// carry, so cross-kind comparison would alias zero payloads whenever probe
// slots collide. Symbol payloads compare canonical SymbolIDs, which are
// unique per byte sequence by construction, so equal bytes imply equal refs
// without a byte comparison.
func valueEqual(dst *program.Program, stored schema.ValueKind, ref uint32, candidate schema.ValueKind, symbol schema.SymbolID, integer, timestamp int64, boolean uint8) bool {
	if stored != candidate {
		return false
	}
	switch stored {
	case schema.ValueKindSymbol:
		return ref == uint32(symbol)
	case schema.ValueKindInteger:
		return ref >= 1 && uint64(ref-1) < uint64(len(dst.IntegerValues)) && dst.IntegerValues[ref-1] == integer
	case schema.ValueKindBoolean:
		return ref >= 1 && uint64(ref-1) < uint64(len(dst.BooleanValues)) && dst.BooleanValues[ref-1] == uint64(boolean)
	case schema.ValueKindTimestamp:
		return ref >= 1 && uint64(ref-1) < uint64(len(dst.TimestampValues)) && dst.TimestampValues[ref-1] == timestamp
	}
	return false
}

// appendValuePayload appends one canonical payload to the packed column of
// its kind and returns its one-based reference. Symbol payloads reference the
// canonical SymbolID and append nothing.
func appendValuePayload(dst *program.Program, kind schema.ValueKind, symbol schema.SymbolID, integer, timestamp int64, boolean uint8) (uint32, error) {
	switch kind {
	case schema.ValueKindSymbol:
		if symbol == 0 {
			return 0, ErrInvalidDocument
		}
		return uint32(symbol), nil
	case schema.ValueKindInteger:
		if uint64(len(dst.IntegerValues)) >= uint64(math.MaxUint32) {
			return 0, ErrProgramTooLarge
		}
		ref := uint32(len(dst.IntegerValues)) + 1
		dst.IntegerValues = append(dst.IntegerValues, integer)
		return ref, nil
	case schema.ValueKindBoolean:
		if uint64(len(dst.BooleanValues)) >= uint64(math.MaxUint32) {
			return 0, ErrProgramTooLarge
		}
		ref := uint32(len(dst.BooleanValues)) + 1
		dst.BooleanValues = append(dst.BooleanValues, uint64(boolean))
		return ref, nil
	case schema.ValueKindTimestamp:
		if uint64(len(dst.TimestampValues)) >= uint64(math.MaxUint32) {
			return 0, ErrProgramTooLarge
		}
		ref := uint32(len(dst.TimestampValues)) + 1
		dst.TimestampValues = append(dst.TimestampValues, timestamp)
		return ref, nil
	}
	return 0, ErrInvalidDocument
}

// canonicalizeValues walks AST ValueIDs in ascending order and interns every
// literal by canonical kind and payload. Symbol values are interned first so
// the canonical SymbolID is available as the value payload reference. The
// walk fills the ValueID-to-Program remap columns for every value, so later
// metadata and catalog translation and instruction lowering can translate any
// source ValueID without scanning.
func (l *Lowerer) canonicalizeValues(dst *program.Program, doc *ast.Document) error {
	for i := range doc.ValueKinds {
		id := schema.ValueID(i + 1)
		kind := doc.ValueKinds[i]
		var h uint64
		var symbol schema.SymbolID
		var integer, timestamp int64
		var boolean uint8
		switch kind {
		case schema.ValueKindSymbol:
			b, ok := doc.SymbolValue(id)
			if !ok {
				return ErrInvalidDocument
			}
			sym, err := l.internSymbol(dst, b)
			if err != nil {
				return err
			}
			symbol = sym
			h = valueHash(kind, b, 0, 0, 0)
			l.symbolRemap[i] = symbol
		case schema.ValueKindInteger:
			v, ok := doc.IntegerValue(id)
			if !ok {
				return ErrInvalidDocument
			}
			integer = v
			h = valueHash(kind, nil, v, 0, 0)
		case schema.ValueKindBoolean:
			v, ok := doc.BooleanValue(id)
			if !ok {
				return ErrInvalidDocument
			}
			if v {
				boolean = 1
			}
			h = valueHash(kind, nil, 0, 0, boolean)
		case schema.ValueKindTimestamp:
			v, ok := doc.TimestampValue(id)
			if !ok {
				return ErrInvalidDocument
			}
			timestamp = v
			h = valueHash(kind, nil, 0, v, 0)
		default:
			return ErrInvalidDocument
		}
		valueID, err := l.internValue(dst, h, kind, symbol, integer, timestamp, boolean)
		if err != nil {
			return err
		}
		l.valueRemap[i] = valueID
	}
	return nil
}

// compareOpcode maps an AST CompareOp to the matching Program opcode. An
// invalid operation reports ok=false; the instruction stage rejects it.
func compareOpcode(op ast.CompareOp) (program.Opcode, bool) {
	switch op {
	case ast.CompareOpEqual:
		return program.OpcodeEqual, true
	case ast.CompareOpNotEqual:
		return program.OpcodeNotEqual, true
	case ast.CompareOpIn:
		return program.OpcodeIn, true
	case ast.CompareOpExists:
		return program.OpcodeExists, true
	case ast.CompareOpLess:
		return program.OpcodeLess, true
	case ast.CompareOpLessEqual:
		return program.OpcodeLessEqual, true
	case ast.CompareOpGreater:
		return program.OpcodeGreater, true
	case ast.CompareOpGreaterEqual:
		return program.OpcodeGreaterEqual, true
	}
	return 0, false
}

// canonicalValue returns the canonical Program ValueID of a source AST
// ValueID through the constant-stage remap column. The remap is filled for
// every AST ValueID by lowerConstants, so a zero or out-of-range source ID
// or a missing remap row reports corrupt input.
func (l *Lowerer) canonicalValue(id schema.ValueID) (schema.ValueID, error) {
	if id == 0 || uint64(id) > uint64(len(l.valueRemap)) {
		return 0, ErrInvalidDocument
	}
	canonical := l.valueRemap[id-1]
	if canonical == 0 {
		return 0, ErrInvalidDocument
	}
	return canonical, nil
}

// freezeSymbolSlots freezes the immutable open-address symbol probe table
// over the canonical symbol slab. The slot count is the smallest power of two
// at least twice the symbol count, so occupancy never exceeds 50% and every
// frozen SymbolID stays within ProgramSymbolCount, reserving larger IDs for
// the Task 13 batch extension interner.
func freezeSymbolSlots(dst *program.Program) error {
	count := len(dst.SymbolStarts)
	if uint64(count) >= uint64(math.MaxUint32) {
		return ErrProgramTooLarge
	}
	size := slotSize(count)
	if size == 0 {
		return ErrProgramTooLarge
	}
	dst.SymbolHashes = resizeSlots(dst.SymbolHashes, size)
	dst.SymbolIDs = resizeSlots(dst.SymbolIDs, size)
	mask := uint64(size - 1)
	for id := 1; id <= count; id++ {
		b, ok := dst.Symbol(schema.SymbolID(id))
		if !ok {
			return ErrInvalidDocument
		}
		h := schema.HashSymbol(b)
		slot := int(h & mask)
		probes := 0
		for ; probes < size; probes++ {
			if dst.SymbolIDs[slot] == 0 {
				break
			}
			slot = (slot + 1) & int(mask)
		}
		if probes >= size {
			return ErrProgramTooLarge
		}
		dst.SymbolHashes[slot] = h
		dst.SymbolIDs[slot] = schema.SymbolID(id)
	}
	dst.ProgramSymbolCount = uint32(count)
	return nil
}
