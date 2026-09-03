package diff

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/schema"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func allKindFieldSchema() FieldSchema {
	fields := nativeFieldSchema()
	fields.Fields = append(fields.Fields,
		FieldSpec{Name: "test.symbol", Kind: FieldKindString, Group: FieldGroupContext},
		FieldSpec{Name: "test.integer", Kind: FieldKindInteger, Group: FieldGroupContext},
		FieldSpec{Name: "test.boolean", Kind: FieldKindBoolean, Group: FieldGroupContext},
		FieldSpec{Name: "test.timestamp", Kind: FieldKindTimestamp, Group: FieldGroupContext},
		FieldSpec{Name: "test.presence", Kind: FieldKindPresence, Group: FieldGroupContext},
	)
	return fields
}

func allKindDomainAndPlan() (Domain, searchPlan) {
	domain := Domain{MaxCandidates: 3, BatchRows: 3}
	domain.Fields = []FieldDomain{
		{Name: "test.symbol", Kind: FieldKindString, Closed: true, Values: []Value{
			{Kind: FieldKindString, State: ValueMissing},
			{Kind: FieldKindString, State: ValuePresent},
			{Kind: FieldKindString, State: ValuePresent, String: "candidate-extension"},
		}},
		{Name: "test.integer", Kind: FieldKindInteger, Closed: true, Values: []Value{
			{Kind: FieldKindInteger, State: ValueMissing},
			{Kind: FieldKindInteger, State: ValuePresent, Integer: math.MinInt64},
			{Kind: FieldKindInteger, State: ValuePresent, Integer: math.MaxInt64},
		}},
		{Name: "test.boolean", Kind: FieldKindBoolean, Closed: true, Values: []Value{
			{Kind: FieldKindBoolean, State: ValueMissing},
			{Kind: FieldKindBoolean, State: ValuePresent},
			{Kind: FieldKindBoolean, State: ValuePresent, Boolean: true},
		}},
		{Name: "test.timestamp", Kind: FieldKindTimestamp, Closed: true, Values: []Value{
			{Kind: FieldKindTimestamp, State: ValueMissing},
			{Kind: FieldKindTimestamp, State: ValuePresent, Integer: math.MinInt64},
			{Kind: FieldKindTimestamp, State: ValuePresent, Integer: math.MaxInt64},
		}},
		{Name: "test.presence", Kind: FieldKindPresence, Closed: true, Values: []Value{
			{Kind: FieldKindPresence, State: ValueMissing},
			{Kind: FieldKindPresence, State: ValuePresent},
			{Kind: FieldKindPresence, State: ValuePresent},
		}},
	}
	plan := searchPlan{
		fieldRows:     []uint32{0, 1, 2, 3, 4},
		optionCounts:  []uint32{3, 3, 3, 3, 3},
		strides:       []uint64{1, 1, 1, 1, 1},
		oldFieldIDs:   []schema.FieldID{8, 9, 10, 11, 12},
		newFieldIDs:   []schema.FieldID{8, 9, 10, 11, 12},
		cardinality:   3,
		evidenceCount: 1,
	}
	return domain, plan
}

func TestCandidateBatchMaterializesAllKindsAndProgramLocalSymbols(t *testing.T) {
	oldSource := []byte(nornrune.Source())
	newSource := []byte(strings.Replace(nornrune.Source(), `"name": "nornrune"`, `"name": "candidate-extension"`, 1))
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair(oldSource, newSource, allKindFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	domain, plan := allKindDomainAndPlan()
	var materializer candidateMaterializer
	var batches candidateBatches
	if err := materializer.materialize(&batches, oldProgram, newProgram, &plan, domain, 0, 3); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	for _, field := range plan.oldFieldIDs {
		if batches.old.Present(field, 0) {
			t.Fatalf("missing field %d marked present", field)
		}
		if !batches.old.Present(field, 1) || !batches.old.Present(field, 2) {
			t.Fatalf("present field %d missing", field)
		}
	}
	_, symbolColumn, _ := oldProgram.FieldIndex.Lookup(plan.oldFieldIDs[0])
	oldEmpty, _ := batches.old.Symbol(symbolColumn, 1)
	oldExtension, _ := batches.old.Symbol(symbolColumn, 2)
	newExtension, _ := batches.new.Symbol(symbolColumn, 2)
	if value, ok := materializer.oldBuilder.Symbol(oldEmpty); !ok || len(value) != 0 {
		t.Fatalf("empty symbol: got %q, ok=%v", value, ok)
	}
	if value, ok := materializer.oldBuilder.Symbol(oldExtension); !ok || string(value) != "candidate-extension" {
		t.Fatalf("old extension symbol: got %q, ok=%v", value, ok)
	}
	if oldExtension == newExtension {
		t.Fatalf("distinct Program namespaces reused symbol ID %d", oldExtension)
	}

	_, integerColumn, _ := oldProgram.FieldIndex.Lookup(plan.oldFieldIDs[1])
	if got, _ := batches.old.Integer(integerColumn, 1); got != math.MinInt64 {
		t.Fatalf("minimum integer: got %d", got)
	}
	if got, _ := batches.old.Integer(integerColumn, 2); got != math.MaxInt64 {
		t.Fatalf("maximum integer: got %d", got)
	}
	_, booleanColumn, _ := oldProgram.FieldIndex.Lookup(plan.oldFieldIDs[2])
	if batches.old.Boolean(booleanColumn, 1) || !batches.old.Boolean(booleanColumn, 2) {
		t.Fatal("Boolean values were not materialized")
	}
	_, timestampColumn, _ := oldProgram.FieldIndex.Lookup(plan.oldFieldIDs[3])
	if got, _ := batches.old.Timestamp(timestampColumn, 1); got != math.MinInt64 {
		t.Fatalf("minimum timestamp: got %d", got)
	}
	if got, _ := batches.old.Timestamp(timestampColumn, 2); got != math.MaxInt64 {
		t.Fatalf("maximum timestamp: got %d", got)
	}
}

func TestCandidateBatchMaterializesEvidenceCSRAndClearsPoison(t *testing.T) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	domain := Domain{
		EvidenceSets: []EvidenceSet{
			{},
			{Records: []Evidence{
				{Kind: "approval_record", State: "stale", Subject: "subject-a", Scope: "scope-a", Timing: "before"},
				{Kind: "approval_record", State: "unclear"},
				{Kind: "approval_record", State: "conflicting"},
			}},
		},
		MaxCandidates: 2,
		BatchRows:     2,
	}
	plan := searchPlan{cardinality: 2, evidenceCount: 2}
	var materializer candidateMaterializer
	var batches candidateBatches
	if err := materializer.materialize(&batches, oldProgram, newProgram, &plan, domain, 0, 2); err != nil {
		t.Fatalf("materialize evidence: %v", err)
	}
	if start, end, ok := batches.old.EvidenceRange(0); !ok || start != 0 || end != 0 {
		t.Fatalf("empty evidence range: (%d,%d,%v)", start, end, ok)
	}
	if start, end, ok := batches.old.EvidenceRange(1); !ok || start != 0 || end != 3 {
		t.Fatalf("three-record evidence range: (%d,%d,%v)", start, end, ok)
	}
	if len(batches.old.Evidence.IDs) != 3 || len(batches.old.EvidenceRefs) != 3 {
		t.Fatalf("evidence shape: rows=%d refs=%d", len(batches.old.Evidence.IDs), len(batches.old.EvidenceRefs))
	}

	if err := materializer.materialize(&batches, oldProgram, newProgram, &plan, domain, 0, 1); err != nil {
		t.Fatalf("reuse empty evidence: %v", err)
	}
	if len(batches.old.Evidence.IDs) != 0 || len(batches.old.EvidenceRefs) != 0 || batches.old.RequestIDs[0] != 1 {
		t.Fatalf("reused batch retained poison: %+v", batches.old)
	}
}

func TestCandidateBatchFiltersEvidenceThroughEachProgramCatalog(t *testing.T) {
	oldSource := []byte(nornrune.Source())
	newSource := []byte(strings.ReplaceAll(nornrune.Source(), "approval_record", "security_review"))
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair(oldSource, newSource, nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	domain := Domain{
		EvidenceSets: []EvidenceSet{{Records: []Evidence{
			{Kind: "approval_record", State: "valid"},
			{Kind: "security_review", State: "valid"},
			{Kind: "execution_environment_attestation", State: "verified"},
		}}},
		MaxCandidates: 1,
		BatchRows:     1,
	}
	plan := searchPlan{cardinality: 1, evidenceCount: 1}
	var materializer candidateMaterializer
	var batches candidateBatches
	if err := materializer.materialize(&batches, oldProgram, newProgram, &plan, domain, 0, 1); err != nil {
		t.Fatalf("materialize evidence: %v", err)
	}
	if len(batches.old.Evidence.Kinds) != 2 || len(batches.new.Evidence.Kinds) != 2 {
		t.Fatalf("evidence rows = old %d, new %d", len(batches.old.Evidence.Kinds), len(batches.new.Evidence.Kinds))
	}
	oldKind, _ := catalogID(oldProgram, oldProgram.EvidenceKindNames, "approval_record")
	newKind, _ := catalogID(newProgram, newProgram.EvidenceKindNames, "security_review")
	if batches.old.Evidence.Kinds[0] != schema.EvidenceKindID(oldKind) ||
		batches.new.Evidence.Kinds[0] != schema.EvidenceKindID(newKind) {
		t.Fatalf("filtered kinds = old %v, new %v", batches.old.Evidence.Kinds, batches.new.Evidence.Kinds)
	}
	if len(batches.old.EvidenceRefs) != 2 || len(batches.new.EvidenceRefs) != 2 ||
		batches.old.EvidenceOffsets[1] != 2 || batches.new.EvidenceOffsets[1] != 2 {
		t.Fatalf("filtered CSR = old (%v,%v), new (%v,%v)",
			batches.old.EvidenceOffsets, batches.old.EvidenceRefs,
			batches.new.EvidenceOffsets, batches.new.EvidenceRefs)
	}
	if !slices.Equal(batches.old.Evidence.IDs, []schema.EvidenceID{1, 3}) ||
		!slices.Equal(batches.new.Evidence.IDs, []schema.EvidenceID{2, 3}) {
		t.Fatalf("filtered evidence IDs = old %v, new %v", batches.old.Evidence.IDs, batches.new.Evidence.IDs)
	}
}

func TestCandidateBatchRejectsExpandedEvidenceAboveSafetyCeiling(t *testing.T) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	records := make([]Evidence, 1<<17)
	for row := range records {
		records[row] = Evidence{Kind: "approval_record", State: "valid"}
	}
	domain := Domain{
		EvidenceSets:  []EvidenceSet{{Records: records}},
		MaxCandidates: 3,
		BatchRows:     3,
	}
	plan := searchPlan{cardinality: 3, evidenceCount: 1}
	var materializer candidateMaterializer
	var batches candidateBatches
	if err := materializer.materialize(&batches, oldProgram, newProgram, &plan, domain, 0, 3); !errors.Is(err, ErrCandidateBudget) {
		t.Fatalf("materialize expanded evidence: got %v, want %v", err, ErrCandidateBudget)
	}
	if cap(materializer.oldEvidence.refs) != 0 || cap(materializer.newEvidence.refs) != 0 {
		t.Fatalf("expanded evidence allocated refs: old=%d new=%d", cap(materializer.oldEvidence.refs), cap(materializer.newEvidence.refs))
	}
}

func TestCandidateBatchRejectsUnknownEvidenceAboveSourceCeiling(t *testing.T) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	records := make([]Evidence, int(maxCandidateEvidenceRows)+1)
	for row := range records {
		records[row] = Evidence{Kind: "unknown_kind", State: "unknown_state"}
	}
	domain := Domain{EvidenceSets: []EvidenceSet{{Records: records}}, MaxCandidates: 1, BatchRows: 1}
	plan := searchPlan{cardinality: 1, evidenceCount: 1}
	var materializer candidateMaterializer
	var batches candidateBatches
	if err := materializer.materialize(&batches, oldProgram, newProgram, &plan, domain, 0, 1); !errors.Is(err, ErrCandidateBudget) {
		t.Fatalf("materialize unknown source evidence: got %v, want %v", err, ErrCandidateBudget)
	}
}

func TestCandidateBatchRejectsTooManyEvidenceScenarios(t *testing.T) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	domain := Domain{
		EvidenceSets:  make([]EvidenceSet, int(MaxBatchRows)+1),
		MaxCandidates: uint64(MaxBatchRows) + 1,
		BatchRows:     1,
	}
	plan := searchPlan{cardinality: uint64(MaxBatchRows) + 1, evidenceCount: MaxBatchRows + 1}
	var materializer candidateMaterializer
	var batches candidateBatches
	if err := materializer.materialize(&batches, oldProgram, newProgram, &plan, domain, 0, 1); !errors.Is(err, ErrCandidateBudget) {
		t.Fatalf("materialize evidence scenarios: got %v, want %v", err, ErrCandidateBudget)
	}
}

type cancelAfterEvidenceChecks struct {
	context.Context
	checks uint32
}

func (ctx *cancelAfterEvidenceChecks) Err() error {
	ctx.checks++
	if ctx.checks > 2 {
		return context.Canceled
	}
	return nil
}

func TestCandidateBatchChecksCancellationWhileIndexingEvidence(t *testing.T) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	records := make([]Evidence, 128)
	for row := range records {
		records[row] = Evidence{Kind: "unknown_kind", State: "unknown_state"}
	}
	domain := Domain{EvidenceSets: []EvidenceSet{{Records: records}}, MaxCandidates: 1, BatchRows: 1}
	plan := searchPlan{cardinality: 1, evidenceCount: 1}
	ctx := &cancelAfterEvidenceChecks{Context: context.Background()}
	var materializer candidateMaterializer
	var batches candidateBatches
	if err := materializer.materializeContext(ctx, &batches, oldProgram, newProgram, &plan, domain, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("materialize cancellation during evidence indexing: got %v, want %v", err, context.Canceled)
	}
}

func TestCandidateBatchErrorLeavesDestinationUnchanged(t *testing.T) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	plan := searchPlan{
		fieldRows: []uint32{0}, optionCounts: []uint32{1}, strides: []uint64{1},
		oldFieldIDs: []schema.FieldID{1}, newFieldIDs: []schema.FieldID{1},
		cardinality: 1, evidenceCount: 1,
	}
	domain := Domain{
		Fields: []FieldDomain{{
			Name: "requester.team", Kind: FieldKindString, Closed: true,
			Values: []Value{{State: ValuePresent, Kind: FieldKindInvalid}},
		}},
		MaxCandidates: 1,
		BatchRows:     1,
	}
	want := candidateBatches{old: eval.Batch{Rows: 99}, new: eval.Batch{Rows: 100}}
	got := want
	var materializer candidateMaterializer
	if err := materializer.materialize(&got, oldProgram, newProgram, &plan, domain, 0, 1); err == nil {
		t.Fatal("materialize invalid candidate: got nil error")
	}
	if got.old.Rows != want.old.Rows || got.new.Rows != want.new.Rows {
		t.Fatalf("destination changed on error: got (%d,%d)", got.old.Rows, got.new.Rows)
	}
}
