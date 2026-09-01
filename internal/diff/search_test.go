package diff

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/sebishogun/nornrune/internal/program"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func nativeDomain(maxCandidates uint64) Domain {
	fields := nativeFieldSchema().Fields
	domain := Domain{
		Fields:        make([]FieldDomain, len(fields)),
		EvidenceSets:  []EvidenceSet{{}, {Records: []Evidence{{Kind: "approval_record", State: "valid"}}}},
		MaxCandidates: maxCandidates,
		BatchRows:     64,
	}
	for row := range fields {
		domain.Fields[row] = FieldDomain{
			Name:   fields[row].Name,
			Kind:   fields[row].Kind,
			Closed: true,
			Values: []Value{
				{Kind: fields[row].Kind, State: ValueMissing},
				{Kind: fields[row].Kind, State: ValuePresent, String: "candidate"},
			},
		}
	}
	return domain
}

func plannedFieldNames(plan *searchPlan, domain Domain) []string {
	names := make([]string, len(plan.fieldRows))
	for row, fieldRow := range plan.fieldRows {
		names[row] = domain.Fields[fieldRow].Name
	}
	return names
}

func TestDependencyPlanOrdersChangedThenReferencedFieldsAndPrunesUnused(t *testing.T) {
	changedSource := strings.ReplaceAll(nornrune.Source(), `"aggregate_counts"`, `"aggregate_totals"`)
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(changedSource), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	domain := nativeDomain(128)
	plan, err := buildSearchPlan(nil, oldProgram, newProgram, domain)
	if err != nil {
		t.Fatalf("build search plan: %v", err)
	}
	want := []string{
		"action.output",
		"action.dataset",
		"action.type",
		"environment.execution_env",
		"environment.usage",
		"requester.trust",
	}
	if got := plannedFieldNames(plan, domain); !slicesEqual(got, want) {
		t.Fatalf("field order: got %v, want %v", got, want)
	}
	if len(plan.changed) != len(want) || plan.changed[0] != 1 {
		t.Fatalf("changed flags: got %v", plan.changed)
	}
	for row := 1; row < len(plan.changed); row++ {
		if plan.changed[row] != 0 {
			t.Fatalf("unchanged field %q marked changed", want[row])
		}
	}
	if plan.evidenceCount != 2 || plan.cardinality != 128 {
		t.Fatalf("plan dimensions: evidence=%d cardinality=%d", plan.evidenceCount, plan.cardinality)
	}
}

func TestDependencyPlanResultChangeMarksAllReferencedFields(t *testing.T) {
	var analyzer Analyzer
	oldProgram, _, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile policy: %v", err)
	}
	newProgram, err := program.Freeze(oldProgram)
	if err != nil {
		t.Fatalf("clone program: %v", err)
	}
	newProgram.Outcomes.Precedence[0]++

	plan, err := buildSearchPlan(nil, oldProgram, &newProgram, nativeDomain(128))
	if err != nil {
		t.Fatalf("build search plan: %v", err)
	}
	want := []string{
		"action.dataset",
		"action.output",
		"action.type",
		"environment.execution_env",
		"environment.usage",
		"requester.trust",
	}
	if got := plannedFieldNames(plan, nativeDomain(128)); !slicesEqual(got, want) {
		t.Fatalf("field order: got %v, want %v", got, want)
	}
	for row, changed := range plan.changed {
		if changed != 1 {
			t.Fatalf("result change left field %q unchanged", want[row])
		}
	}
}

func TestSearchPlanChecksPrunedCardinalityBudget(t *testing.T) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	if plan, err := buildSearchPlan(nil, oldProgram, newProgram, nativeDomain(128)); err != nil || plan.cardinality != 128 {
		t.Fatalf("exact budget: plan=%v err=%v", plan, err)
	}
	if _, err := buildSearchPlan(nil, oldProgram, newProgram, nativeDomain(127)); !errors.Is(err, ErrCandidateBudget) {
		t.Fatalf("exceeded budget: got %v, want %v", err, ErrCandidateBudget)
	}
}

func TestExhaustiveOrderIsDeterministicWithoutDuplicates(t *testing.T) {
	plan := searchPlan{
		fieldRows:     []uint32{4, 2},
		optionCounts:  []uint32{2, 3},
		strides:       []uint64{1, 2},
		cardinality:   12,
		evidenceCount: 2,
	}
	fieldOptions := make([]uint32, 2*12)
	evidenceOptions := make([]uint32, 12)
	if err := plan.generate(context.Background(), fieldOptions, evidenceOptions, 0, 12); err != nil {
		t.Fatalf("generate: %v", err)
	}
	wantFirst := []uint32{0, 1, 0, 1, 0, 1, 0, 1}
	if !uint32sEqual(fieldOptions[:8], wantFirst) {
		t.Fatalf("first radix: got %v, want %v", fieldOptions[:8], wantFirst)
	}
	wantSecond := []uint32{0, 0, 1, 1, 2, 2, 0, 0}
	if !uint32sEqual(fieldOptions[12:20], wantSecond) {
		t.Fatalf("second radix: got %v, want %v", fieldOptions[12:20], wantSecond)
	}
	wantEvidence := []uint32{0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1}
	if !uint32sEqual(evidenceOptions, wantEvidence) {
		t.Fatalf("evidence radix: got %v, want %v", evidenceOptions, wantEvidence)
	}
	seen := make([]bool, 12)
	for row := uint32(0); row < 12; row++ {
		key := fieldOptions[row] + 2*fieldOptions[12+row] + 6*evidenceOptions[row]
		if seen[key] {
			t.Fatalf("duplicate candidate key %d", key)
		}
		seen[key] = true
	}
}

func TestSearchGenerationHandlesBatchTailsAndCancellation(t *testing.T) {
	plan := searchPlan{fieldRows: []uint32{0}, optionCounts: []uint32{65}, strides: []uint64{1}, cardinality: 65, evidenceCount: 1}
	for _, rows := range []uint32{1, 63, 64, 65} {
		fields := make([]uint32, rows)
		evidence := make([]uint32, rows)
		if err := plan.generate(context.Background(), fields, evidence, 0, rows); err != nil {
			t.Fatalf("generate %d rows: %v", rows, err)
		}
		if fields[rows-1] != rows-1 {
			t.Fatalf("tail %d: got final option %d", rows, fields[rows-1])
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := plan.generate(cancelled, make([]uint32, 1), make([]uint32, 1), 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled generation: got %v", err)
	}
}

func TestSearchPlanRejectsCardinalityBeyondRequestIDSpace(t *testing.T) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatal(err)
	}
	domain := nativeDomain(math.MaxUint64)
	for row := range domain.Fields {
		if domain.Fields[row].Name == "action.output" {
			domain.Fields[row].Values = make([]Value, 65536)
			for option := range domain.Fields[row].Values {
				domain.Fields[row].Values[option] = Value{Kind: FieldKindString, State: ValuePresent, String: "value"}
			}
			domain.Fields[row].Values[0] = Value{Kind: FieldKindString, State: ValueMissing}
		}
		if domain.Fields[row].Name == "action.type" {
			domain.Fields[row].Values = make([]Value, 4097)
			for option := range domain.Fields[row].Values {
				domain.Fields[row].Values[option] = Value{Kind: FieldKindString, State: ValuePresent, String: "value"}
			}
			domain.Fields[row].Values[0] = Value{Kind: FieldKindString, State: ValueMissing}
		}
	}
	if _, err := buildSearchPlan(nil, oldProgram, newProgram, domain); !errors.Is(err, ErrCandidateBudget) {
		t.Fatalf("oversized request ID space: got %v", err)
	}
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for row := range left {
		if left[row] != right[row] {
			return false
		}
	}
	return true
}

func uint32sEqual(left, right []uint32) bool {
	if len(left) != len(right) {
		return false
	}
	for row := range left {
		if left[row] != right[row] {
			return false
		}
	}
	return true
}
