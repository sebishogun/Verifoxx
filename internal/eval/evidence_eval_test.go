package eval

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"testing"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func evidenceSymbolTestProgram(names ...string) *program.Program {
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
	}
	p.ProgramSymbolCount = uint32(len(names))
	return p
}

func evidenceStateTestProgram(names ...string) *program.Program {
	p := evidenceSymbolTestProgram(names...)
	for i := range names {
		p.EvidenceStateNames = append(p.EvidenceStateNames, schema.SymbolID(i+1))
	}
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

const (
	testEvidenceKindApproval schema.EvidenceKindID = iota + 1
	testEvidenceKindOther
)

const (
	testEvidenceStateValid schema.EvidenceStateID = iota + 1
	testEvidenceStateStale
	testEvidenceStateUnclear
	testEvidenceStateUnverifiable
	testEvidenceStateInvalid
	testEvidenceStateConflicting
	testEvidenceStateRejected
)

const (
	testEvidenceSubjectA schema.SymbolID = 10 + iota
	testEvidenceSubjectB
	testEvidenceScopeA
	testEvidenceScopeB
	testEvidenceTimingA
	testEvidenceTimingB
)

func evidenceEvalTestProgram() *program.Program {
	p := evidenceSymbolTestProgram(
		"approval", "other",
		"valid", "stale", "unclear", "unverifiable", "invalid", "conflicting", "rejected",
		"subject-a", "subject-b", "scope-a", "scope-b", "timing-a", "timing-b",
	)
	p.EvidenceKindNames = []schema.SymbolID{1, 2}
	p.EvidenceStateNames = []schema.SymbolID{3, 4, 5, 6, 7, 8, 9}
	return p
}

func evidenceEvalBatch(t testing.TB, p *program.Program, rows uint32, records []EvidenceRecord, offsets, refs []uint32) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, rows, uint32(len(records)), uint32(len(refs))); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for row := uint32(0); row < rows; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatalf("SetRequestID(%d): %v", row, err)
		}
	}
	for row, record := range records {
		if err := builder.SetEvidence(uint32(row), record); err != nil {
			t.Fatalf("SetEvidence(%d): %v", row, err)
		}
	}
	if err := builder.SetEvidenceCSR(offsets, refs); err != nil {
		t.Fatalf("SetEvidenceCSR: %v", err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return batch
}

func evidenceRecord(id schema.EvidenceID, kind schema.EvidenceKindID, state schema.EvidenceStateID) EvidenceRecord {
	return EvidenceRecord{ID: id, Kind: kind, State: state}
}

func assertEvidenceRow(t testing.TB, dst truth.Planes, reasons ReasonPlanes, rows, row uint32, positive, negative bool, wantReasons truth.ReasonMask) {
	t.Helper()
	word, bit := row>>6, uint64(1)<<(row&63)
	if got := dst.Positive[word]&bit != 0; got != positive {
		t.Errorf("row %d positive = %v, want %v", row, got, positive)
	}
	if got := dst.Negative[word]&bit != 0; got != negative {
		t.Errorf("row %d negative = %v, want %v", row, got, negative)
	}
	for reason := truth.ReasonMissing; reason <= truth.ReasonConflict; reason++ {
		got := reasons.Plane(reason, rows)[word]&bit != 0
		if want := wantReasons.Has(reason); got != want {
			t.Errorf("row %d reason %d = %v, want %v", row, reason, got, want)
		}
	}
}

func TestEvalEvidenceStatesAndMissingKind(t *testing.T) {
	p := evidenceEvalTestProgram()
	var states EvidenceStateIndex
	if err := states.Bind(p); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	records := []EvidenceRecord{
		evidenceRecord(1, testEvidenceKindApproval, testEvidenceStateValid),
		evidenceRecord(2, testEvidenceKindApproval, testEvidenceStateStale),
		evidenceRecord(3, testEvidenceKindApproval, testEvidenceStateUnclear),
		evidenceRecord(4, testEvidenceKindApproval, testEvidenceStateUnverifiable),
		evidenceRecord(5, testEvidenceKindApproval, testEvidenceStateInvalid),
		evidenceRecord(6, testEvidenceKindApproval, testEvidenceStateConflicting),
		evidenceRecord(7, testEvidenceKindApproval, testEvidenceStateRejected),
		evidenceRecord(8, testEvidenceKindOther, testEvidenceStateValid),
	}
	offsets := []uint32{0, 1, 2, 3, 4, 5, 6, 7, 8}
	refs := []uint32{0, 1, 2, 3, 4, 5, 6, 7}
	batch := evidenceEvalBatch(t, p, 8, records, offsets, refs)
	dst, reasons := makeLeafOutputs(batch.Rows)
	evalEvidence(dst, reasons, batch, p, &states, EvidencePredicate{Kind: testEvidenceKindApproval, State: testEvidenceStateValid})
	wants := []struct {
		positive, negative bool
		reasons            truth.ReasonMask
	}{
		{positive: true},
		{reasons: truth.ReasonBit(truth.ReasonStale)},
		{reasons: truth.ReasonBit(truth.ReasonUnclear)},
		{reasons: truth.ReasonBit(truth.ReasonUnverifiable)},
		{reasons: truth.ReasonBit(truth.ReasonInvalid)},
		{positive: true, negative: true, reasons: truth.ReasonBit(truth.ReasonConflict)},
		{negative: true},
		{reasons: truth.ReasonBit(truth.ReasonMissing)},
	}
	for row, want := range wants {
		assertEvidenceRow(t, dst, reasons, batch.Rows, uint32(row), want.positive, want.negative, want.reasons)
	}
}

func TestEvalEvidenceAttributeConstraints(t *testing.T) {
	p := evidenceEvalTestProgram()
	var states EvidenceStateIndex
	if err := states.Bind(p); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	exact := evidenceRecord(1, testEvidenceKindApproval, testEvidenceStateValid)
	exact.Subject, exact.Scope, exact.Timing = testEvidenceSubjectA, testEvidenceScopeA, testEvidenceTimingA
	wrong := evidenceRecord(2, testEvidenceKindApproval, testEvidenceStateValid)
	wrong.Subject, wrong.Scope, wrong.Timing = testEvidenceSubjectB, testEvidenceScopeB, testEvidenceTimingB
	absent := evidenceRecord(3, testEvidenceKindApproval, testEvidenceStateValid)
	batch := evidenceEvalBatch(t, p, 3, []EvidenceRecord{exact, wrong, absent}, []uint32{0, 1, 2, 3}, []uint32{0, 1, 2})
	predicate := EvidencePredicate{
		Kind: testEvidenceKindApproval, State: testEvidenceStateValid,
		Subject: testEvidenceSubjectA, Scope: testEvidenceScopeA, Timing: testEvidenceTimingA,
	}
	dst, reasons := makeLeafOutputs(batch.Rows)
	evalEvidence(dst, reasons, batch, p, &states, predicate)
	assertEvidenceRow(t, dst, reasons, batch.Rows, 0, true, false, 0)
	wantWrong := truth.ReasonBit(truth.ReasonWrongSubject) |
		truth.ReasonBit(truth.ReasonWrongScope) |
		truth.ReasonBit(truth.ReasonWrongTiming)
	assertEvidenceRow(t, dst, reasons, batch.Rows, 1, false, false, wantWrong)
	assertEvidenceRow(t, dst, reasons, batch.Rows, 2, false, false, wantWrong)

	dst, reasons = makeLeafOutputs(batch.Rows)
	evalEvidence(dst, reasons, batch, p, &states, EvidencePredicate{Kind: testEvidenceKindApproval, State: testEvidenceStateValid})
	for row := uint32(0); row < batch.Rows; row++ {
		assertEvidenceRow(t, dst, reasons, batch.Rows, row, true, false, 0)
	}
}

func TestEvalEvidenceReducesMultipleRecords(t *testing.T) {
	p := evidenceEvalTestProgram()
	var states EvidenceStateIndex
	if err := states.Bind(p); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	records := []EvidenceRecord{
		evidenceRecord(1, testEvidenceKindApproval, testEvidenceStateValid),
		evidenceRecord(2, testEvidenceKindApproval, testEvidenceStateRejected),
		evidenceRecord(3, testEvidenceKindApproval, testEvidenceStateStale),
		evidenceRecord(4, testEvidenceKindOther, testEvidenceStateValid),
	}
	batch := evidenceEvalBatch(t, p, 4, records, []uint32{0, 2, 4, 6, 7}, []uint32{1, 0, 2, 0, 0, 0, 3})
	dst, reasons := makeLeafOutputs(batch.Rows)
	evalEvidence(dst, reasons, batch, p, &states, EvidencePredicate{Kind: testEvidenceKindApproval, State: testEvidenceStateValid})
	assertEvidenceRow(t, dst, reasons, batch.Rows, 0, true, true, truth.ReasonBit(truth.ReasonConflict))
	assertEvidenceRow(t, dst, reasons, batch.Rows, 1, true, false, truth.ReasonBit(truth.ReasonStale))
	assertEvidenceRow(t, dst, reasons, batch.Rows, 2, true, false, 0)
	assertEvidenceRow(t, dst, reasons, batch.Rows, 3, false, false, truth.ReasonBit(truth.ReasonMissing))
}

func TestEvalEvidenceWordBoundaries(t *testing.T) {
	for _, rows := range []uint32{0, 1, 63, 64, 65} {
		t.Run(strconv.FormatUint(uint64(rows), 10), func(t *testing.T) {
			p := evidenceEvalTestProgram()
			var states EvidenceStateIndex
			if err := states.Bind(p); err != nil {
				t.Fatalf("Bind: %v", err)
			}
			records := []EvidenceRecord{evidenceRecord(1, testEvidenceKindApproval, testEvidenceStateValid)}
			offsets := make([]uint32, rows+1)
			refs := make([]uint32, rows)
			for row := uint32(0); row < rows; row++ {
				offsets[row+1] = row + 1
			}
			batch := evidenceEvalBatch(t, p, rows, records, offsets, refs)
			dst, reasons := makeLeafOutputs(rows)
			for i := range dst.Positive {
				dst.Positive[i], dst.Negative[i] = ^uint64(0), ^uint64(0)
			}
			for i := range reasons.Words {
				reasons.Words[i] = ^uint64(0)
			}
			evalEvidence(dst, reasons, batch, p, &states, EvidencePredicate{Kind: testEvidenceKindApproval, State: testEvidenceStateValid})
			for row := uint32(0); row < rows; row++ {
				assertEvidenceRow(t, dst, reasons, rows, row, true, false, 0)
			}
			if rows&63 != 0 && len(dst.Positive) != 0 {
				valid := uint64(1)<<(rows&63) - 1
				last := len(dst.Positive) - 1
				if dst.Positive[last]&^valid != 0 || dst.Negative[last]&^valid != 0 {
					t.Fatal("truth tail is dirty")
				}
				for reason := truth.ReasonMissing; reason <= truth.ReasonConflict; reason++ {
					if reasons.Plane(reason, rows)[last]&^valid != 0 {
						t.Fatalf("reason %d tail is dirty", reason)
					}
				}
			}
		})
	}
}

func TestEvalEvidenceRejectsMalformedInputsBeforeMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Batch, *program.Program, *EvidenceStateIndex, *EvidencePredicate)
	}{
		{"short offsets", func(batch *Batch, _ *program.Program, _ *EvidenceStateIndex, _ *EvidencePredicate) {
			batch.EvidenceOffsets = batch.EvidenceOffsets[:1]
		}},
		{"bad first offset", func(batch *Batch, _ *program.Program, _ *EvidenceStateIndex, _ *EvidencePredicate) {
			batch.EvidenceOffsets[0] = 1
		}},
		{"bad final offset", func(batch *Batch, _ *program.Program, _ *EvidenceStateIndex, _ *EvidencePredicate) {
			batch.EvidenceOffsets[1] = 0
		}},
		{"bad reference", func(batch *Batch, _ *program.Program, _ *EvidenceStateIndex, _ *EvidencePredicate) {
			batch.EvidenceRefs[0] = math.MaxUint32
		}},
		{"short states", func(batch *Batch, _ *program.Program, _ *EvidenceStateIndex, _ *EvidencePredicate) {
			batch.Evidence.States = batch.Evidence.States[:0]
		}},
		{"unbound states", func(_ *Batch, _ *program.Program, states *EvidenceStateIndex, _ *EvidencePredicate) {
			*states = EvidenceStateIndex{}
		}},
		{"zero kind", func(_ *Batch, _ *program.Program, _ *EvidenceStateIndex, predicate *EvidencePredicate) {
			predicate.Kind = 0
		}},
		{"bad kind", func(_ *Batch, _ *program.Program, _ *EvidenceStateIndex, predicate *EvidencePredicate) {
			predicate.Kind = 99
		}},
		{"zero state", func(_ *Batch, _ *program.Program, _ *EvidenceStateIndex, predicate *EvidencePredicate) {
			predicate.State = 0
		}},
		{"bad state", func(_ *Batch, _ *program.Program, _ *EvidenceStateIndex, predicate *EvidencePredicate) {
			predicate.State = 99
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := evidenceEvalTestProgram()
			var states EvidenceStateIndex
			if err := states.Bind(p); err != nil {
				t.Fatalf("Bind: %v", err)
			}
			batch := evidenceEvalBatch(t, p, 1,
				[]EvidenceRecord{evidenceRecord(1, testEvidenceKindApproval, testEvidenceStateValid)},
				[]uint32{0, 1}, []uint32{0})
			predicate := EvidencePredicate{Kind: testEvidenceKindApproval, State: testEvidenceStateValid}
			tc.mutate(&batch, p, &states, &predicate)
			dst := truth.Planes{Positive: []uint64{11}, Negative: []uint64{12}}
			reasons := ReasonPlanes{Words: slices.Repeat([]uint64{13}, truth.ReasonCount)}
			requirePanic(t, func() { evalEvidence(dst, reasons, batch, p, &states, predicate) })
			if dst.Positive[0] != 11 || dst.Negative[0] != 12 ||
				!slices.Equal(reasons.Words, slices.Repeat([]uint64{13}, truth.ReasonCount)) {
				t.Fatal("malformed input mutated output")
			}
		})
	}
}

func TestEvidenceValidationSplitRejectsMalformed(t *testing.T) {
	p := evidenceEvalTestProgram()
	var states EvidenceStateIndex
	if err := states.Bind(p); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	batch := evidenceEvalBatch(t, p, 1,
		[]EvidenceRecord{evidenceRecord(1, testEvidenceKindApproval, testEvidenceStateValid)},
		[]uint32{0, 1}, []uint32{0})
	requireEvidenceBatch(batch, p, &states)

	badBatch := batch
	badBatch.Evidence.States = badBatch.Evidence.States[:0]
	requirePanic(t, func() { requireEvidenceBatch(badBatch, p, &states) })

	dst := truth.Planes{Positive: []uint64{11}, Negative: []uint64{12}}
	reasons := ReasonPlanes{Words: slices.Repeat([]uint64{13}, truth.ReasonCount)}
	requirePanic(t, func() {
		evalEvidenceValidated(dst, reasons, batch, p, &states, EvidencePredicate{State: testEvidenceStateValid})
	})
	if dst.Positive[0] != 11 || dst.Negative[0] != 12 ||
		!slices.Equal(reasons.Words, slices.Repeat([]uint64{13}, truth.ReasonCount)) {
		t.Fatal("invalid validated predicate mutated output")
	}
}

func TestEvalEvidenceValidatedMatchesWrapper(t *testing.T) {
	p := evidenceEvalTestProgram()
	var states EvidenceStateIndex
	if err := states.Bind(p); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	records := []EvidenceRecord{
		evidenceRecord(1, testEvidenceKindApproval, testEvidenceStateValid),
		evidenceRecord(2, testEvidenceKindApproval, testEvidenceStateStale),
		evidenceRecord(3, testEvidenceKindApproval, testEvidenceStateConflicting),
	}
	batch := evidenceEvalBatch(t, p, 3, records, []uint32{0, 1, 2, 3}, []uint32{0, 1, 2})
	predicate := EvidencePredicate{Kind: testEvidenceKindApproval, State: testEvidenceStateValid}
	wantTruth, wantReasons := makeLeafOutputs(batch.Rows)
	evalEvidence(wantTruth, wantReasons, batch, p, &states, predicate)

	requireEvidenceBatch(batch, p, &states)
	gotTruth, gotReasons := makeLeafOutputs(batch.Rows)
	for i := range gotTruth.Positive {
		gotTruth.Positive[i], gotTruth.Negative[i] = math.MaxUint64, math.MaxUint64
	}
	for i := range gotReasons.Words {
		gotReasons.Words[i] = math.MaxUint64
	}
	evalEvidenceValidated(gotTruth, gotReasons, batch, p, &states, predicate)
	if !slices.Equal(gotTruth.Positive, wantTruth.Positive) ||
		!slices.Equal(gotTruth.Negative, wantTruth.Negative) ||
		!slices.Equal(gotReasons.Words, wantReasons.Words) {
		t.Fatalf("validated result = %#x/%#x %#x, want %#x/%#x %#x",
			gotTruth.Positive, gotTruth.Negative, gotReasons.Words,
			wantTruth.Positive, wantTruth.Negative, wantReasons.Words)
	}
}
