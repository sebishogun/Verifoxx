package eval

import (
	"errors"
	"slices"
	"testing"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func evidenceStateTestProgram(names ...string) *program.Program {
	slots := 4
	for slots < 2*len(names) {
		slots <<= 1
	}
	p := &program.Program{
		SymbolHashes: make([]uint64, slots),
		SymbolIDs:    make([]schema.SymbolID, slots),
	}
	mask := uint64(slots - 1)
	for i, name := range names {
		id := schema.SymbolID(i + 1)
		p.SymbolStarts = append(p.SymbolStarts, uint32(len(p.SymbolBytes)))
		p.SymbolLengths = append(p.SymbolLengths, uint32(len(name)))
		p.SymbolBytes = append(p.SymbolBytes, name...)
		hash := schema.HashSymbol([]byte(name))
		slot := int(hash & mask)
		for p.SymbolIDs[slot] != 0 {
			slot = (slot + 1) & int(mask)
		}
		p.SymbolHashes[slot] = hash
		p.SymbolIDs[slot] = id
		p.EvidenceStateNames = append(p.EvidenceStateNames, id)
	}
	p.ProgramSymbolCount = uint32(len(names))
	return p
}

func TestEvidenceStateIndexClassifiesArbitraryCatalogOrder(t *testing.T) {
	p := evidenceStateTestProgram(
		"approved", "conflicting", "stale", "valid", "invalid",
		"unclear", "unverifiable", "conflict",
	)
	var index EvidenceStateIndex
	if err := index.Bind(p); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	want := []schema.ReasonID{
		0,
		truth.ReasonConflict,
		truth.ReasonStale,
		0,
		truth.ReasonInvalid,
		truth.ReasonUnclear,
		truth.ReasonUnverifiable,
		truth.ReasonConflict,
	}
	for row, reason := range want {
		state := schema.EvidenceStateID(row + 1)
		if got := index.reason(state); got != reason {
			t.Errorf("state %d reason = %d, want %d", state, got, reason)
		}
	}
}

func TestEvidenceStateIndexRebindClearsStaleClassifications(t *testing.T) {
	var index EvidenceStateIndex
	if err := index.Bind(evidenceStateTestProgram("stale", "invalid", "conflicting")); err != nil {
		t.Fatalf("first Bind: %v", err)
	}
	if err := index.Bind(evidenceStateTestProgram("approved", "verified")); err != nil {
		t.Fatalf("second Bind: %v", err)
	}
	if !slices.Equal(index.reasons, []schema.ReasonID{0, 0}) {
		t.Fatalf("rebound reasons = %v, want resolved zeros", index.reasons)
	}
	requirePanic(t, func() { index.reason(3) })
}

func TestEvidenceStateIndexSameProgramBindReusesStorage(t *testing.T) {
	p := evidenceStateTestProgram("valid", "stale")
	var index EvidenceStateIndex
	if err := index.Bind(p); err != nil {
		t.Fatalf("first Bind: %v", err)
	}
	storage := &index.reasons[0]
	if err := index.Bind(p); err != nil {
		t.Fatalf("second Bind: %v", err)
	}
	if &index.reasons[0] != storage {
		t.Fatal("same-Program Bind replaced storage")
	}
}

func TestEvidenceStateIndexFailedBindIsAtomic(t *testing.T) {
	good := evidenceStateTestProgram("valid", "stale")
	var index EvidenceStateIndex
	if err := index.Bind(good); err != nil {
		t.Fatalf("Bind good: %v", err)
	}
	want := slices.Clone(index.reasons)
	bad := evidenceStateTestProgram("invalid")
	bad.EvidenceStateNames[0] = 99
	if err := index.Bind(bad); !errors.Is(err, ErrInvalidEvidenceProgram) {
		t.Fatalf("Bind bad error = %v, want %v", err, ErrInvalidEvidenceProgram)
	}
	if index.program != good || !slices.Equal(index.reasons, want) {
		t.Fatalf("failed Bind changed index: program=%p reasons=%v", index.program, index.reasons)
	}
	if err := index.Bind(nil); !errors.Is(err, ErrInvalidEvidenceProgram) {
		t.Fatalf("Bind nil error = %v, want %v", err, ErrInvalidEvidenceProgram)
	}
	if index.program != good || !slices.Equal(index.reasons, want) {
		t.Fatal("nil Bind changed index")
	}
}

func TestEvidenceStateIndexRejectsInvalidStateIDs(t *testing.T) {
	var index EvidenceStateIndex
	requirePanic(t, func() { index.reason(0) })
	if err := index.Bind(evidenceStateTestProgram("valid")); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	requirePanic(t, func() { index.reason(2) })
}
