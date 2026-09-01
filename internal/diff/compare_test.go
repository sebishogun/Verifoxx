package diff

import (
	"context"
	"strings"
	"testing"

	"github.com/sebishogun/nornrune/internal/program"
	resultbatch "github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func uniformRiskMatrix(class Outcome, allowed bool) RiskMatrix {
	var matrix RiskMatrix
	for old := Approve; old <= Escalate; old++ {
		for next := Approve; next <= Escalate; next++ {
			transition := Transition{Class: class, Allowed: allowed}
			if old == next {
				transition.Class = Equivalent
			}
			if err := matrix.Set(old, next, transition); err != nil {
				panic(err)
			}
		}
	}
	return matrix
}

func comparisonDomain() Domain {
	values := map[string]string{
		"requester.team":            "trusted_internal",
		"requester.trust":           "external",
		"action.type":               "aggregate_analysis",
		"action.output":             "aggregate_counts",
		"action.dataset":            "protected_dataset",
		"environment.execution_env": "approved_local",
		"environment.usage":         "standard",
	}
	domain := Domain{
		EvidenceSets: []EvidenceSet{
			{},
			{Records: []Evidence{
				{Kind: "approval_record", State: "valid", Timing: "before_execution"},
				{Kind: "execution_environment_attestation", State: "verified"},
			}},
		},
		MaxCandidates: 256,
		BatchRows:     64,
	}
	for _, field := range nativeFieldSchema().Fields {
		domain.Fields = append(domain.Fields, FieldDomain{
			Name: field.Name, Kind: field.Kind, Closed: true,
			Values: []Value{
				{Kind: field.Kind, State: ValueMissing},
				{Kind: field.Kind, State: ValuePresent, String: values[field.Name]},
			},
		})
	}
	return domain
}

func TestCompareClassifiesEveryDecisionTransitionFromCallerMatrix(t *testing.T) {
	classes := []Outcome{Widened, Narrowed, Changed}
	for old := Approve; old <= Escalate; old++ {
		for next := Approve; next <= Escalate; next++ {
			for _, class := range classes {
				matrix := uniformRiskMatrix(Changed, true)
				want := class
				if old == next {
					want = Equivalent
				} else if err := matrix.Set(old, next, Transition{Class: class, Allowed: false}); err != nil {
					t.Fatal(err)
				}
				transition, differing, forbidden, err := classifyEvaluation(matrix, old, next, old != next)
				if err != nil {
					t.Fatalf("%s -> %s: %v", old, next, err)
				}
				if transition != want || differing != (old != next) || forbidden != (old != next) {
					t.Fatalf("%s -> %s: got (%s,%v,%v), want (%s,%v,%v)", old, next, transition, differing, forbidden, want, old != next, old != next)
				}
			}
		}
	}
}

func TestCompareEquivalentCanonicalProgramsTakeIdentityPath(t *testing.T) {
	var analyzer Analyzer
	var result Result
	if err := analyzer.Compare(
		context.Background(), &result,
		[]byte(nornrune.Source()), []byte(nornrune.Source()),
		nativeFieldSchema(), comparisonDomain(), uniformRiskMatrix(Widened, true), nil,
	); err != nil {
		t.Fatalf("compare: %v", err)
	}
	if result.Outcome != Equivalent || result.Candidates != 0 || result.HasCounterexample {
		t.Fatalf("identity result: %+v", result)
	}
}

func TestCompareClassifiesChangeAndOwnsSmallestCounterexample(t *testing.T) {
	oldSource := []byte(nornrune.Source())
	newSource := []byte(strings.ReplaceAll(nornrune.Source(), `"aggregate_counts"`, `"aggregate_totals"`))
	domain := comparisonDomain()
	var analyzer Analyzer
	var result Result
	if err := analyzer.Compare(
		context.Background(), &result, oldSource, newSource,
		nativeFieldSchema(), domain, uniformRiskMatrix(Widened, true), nil,
	); err != nil {
		t.Fatalf("compare: %v", err)
	}
	if result.Outcome != Widened || !result.HasCounterexample || result.Forbidden {
		t.Fatalf("changed result: %+v", result)
	}
	if result.Counterexample.Old.Decision == result.Counterexample.New.Decision {
		t.Fatalf("counterexample decisions did not differ: %+v", result.Counterexample)
	}
	witnessIndex := result.Counterexample.Index
	witnessName := result.Counterexample.Fields[0].Name
	oldSource[0] = 'x'
	newSource[0] = 'x'
	domain.Fields[0].Name = "mutated"
	domain.Fields[0].Values[1].String = "mutated"
	if result.Counterexample.Index != witnessIndex || result.Counterexample.Fields[0].Name != witnessName {
		t.Fatal("counterexample borrowed caller input")
	}

	var inverse Result
	if err := analyzer.Compare(
		context.Background(), &inverse,
		[]byte(nornrune.Source()), []byte(strings.ReplaceAll(nornrune.Source(), `"aggregate_counts"`, `"aggregate_totals"`)),
		nativeFieldSchema(), comparisonDomain(), uniformRiskMatrix(Narrowed, true), nil,
	); err != nil {
		t.Fatalf("inverse compare: %v", err)
	}
	if inverse.Outcome != Narrowed || inverse.Counterexample.Index != witnessIndex {
		t.Fatalf("inverse result: %+v", inverse)
	}
}

func TestCompareReportsForbiddenAndInconclusiveWithoutCorruptingDestination(t *testing.T) {
	changed := []byte(strings.ReplaceAll(nornrune.Source(), `"aggregate_counts"`, `"aggregate_totals"`))
	var analyzer Analyzer
	var result Result
	if err := analyzer.Compare(
		context.Background(), &result, []byte(nornrune.Source()), changed,
		nativeFieldSchema(), comparisonDomain(), uniformRiskMatrix(Changed, false), nil,
	); err != nil {
		t.Fatalf("forbidden compare: %v", err)
	}
	if result.Outcome != Changed || !result.Forbidden {
		t.Fatalf("forbidden result: %+v", result)
	}

	open := comparisonDomain()
	open.Fields[0].Closed = false
	if err := analyzer.Compare(
		context.Background(), &result, []byte(nornrune.Source()), changed,
		nativeFieldSchema(), open, uniformRiskMatrix(Changed, true), nil,
	); err != nil {
		t.Fatalf("open-domain compare: %v", err)
	}
	if result.Outcome == Equivalent {
		t.Fatalf("open domain produced equivalence: %+v", result)
	}

	want := result
	if err := analyzer.Compare(
		context.Background(), &result, []byte(`{`), changed,
		nativeFieldSchema(), comparisonDomain(), uniformRiskMatrix(Changed, true), nil,
	); err == nil {
		t.Fatal("malformed policy compare: got nil error")
	}
	if result.Outcome != want.Outcome || result.Candidates != want.Candidates || result.HasCounterexample != want.HasCounterexample {
		t.Fatal("infrastructure error changed destination")
	}
}

func TestCompareUnsupportedOutcomeAndMissingDimensionAreInconclusive(t *testing.T) {
	var analyzer Analyzer
	var result Result
	nonstandard := []byte(strings.ReplaceAll(nornrune.Source(), `"Approve"`, `"Permit"`))
	if err := analyzer.Compare(
		context.Background(), &result, nonstandard, nonstandard,
		nativeFieldSchema(), comparisonDomain(), uniformRiskMatrix(Changed, true), nil,
	); err != nil || result.Outcome != Inconclusive {
		t.Fatalf("unsupported outcomes: result=%+v err=%v", result, err)
	}
	domain := comparisonDomain()
	for row := range domain.Fields {
		if domain.Fields[row].Name == "requester.trust" {
			domain.Fields = append(domain.Fields[:row], domain.Fields[row+1:]...)
			break
		}
	}
	if err := analyzer.Compare(
		context.Background(), &result, []byte(nornrune.Source()), changedPolicySource(),
		nativeFieldSchema(), domain, uniformRiskMatrix(Changed, true), nil,
	); err != nil || result.Outcome != Inconclusive {
		t.Fatalf("missing dimension: result=%+v err=%v", result, err)
	}
}

func TestCompareUnusedResultCatalogChangeDoesNotFabricateWitness(t *testing.T) {
	var analyzer Analyzer
	oldProgram, _, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatal(err)
	}
	newProgram, err := program.Freeze(oldProgram)
	if err != nil {
		t.Fatal(err)
	}
	newProgram.Outcomes.Precedence[1]++
	oldResults := resultbatch.Batch{Rows: 1, OutcomeIDs: []schema.OutcomeID{1}}
	newResults := resultbatch.Batch{Rows: 1, OutcomeIDs: []schema.OutcomeID{1}}
	for _, offsets := range []*[]uint32{
		&oldResults.RequirementOffsets, &oldResults.DriverOffsets, &oldResults.EvidenceOffsets, &oldResults.ReasonOffsets, &oldResults.RemediationOffsets,
		&newResults.RequirementOffsets, &newResults.DriverOffsets, &newResults.EvidenceOffsets, &newResults.ReasonOffsets, &newResults.RemediationOffsets,
	} {
		*offsets = []uint32{0, 0}
	}
	result := Result{Outcome: Equivalent}
	if err := compareResultBatch(
		&result, oldProgram, &newProgram, &oldResults, &newResults,
		&searchPlan{}, Domain{}, uniformRiskMatrix(Changed, true), 0,
	); err != nil {
		t.Fatalf("compare rows: %v", err)
	}
	if result.Outcome != Equivalent || result.HasCounterexample {
		t.Fatalf("unused result change fabricated witness: %+v", result)
	}
}

func TestCompareDetectsUsedEvidenceIssueTemplateChange(t *testing.T) {
	var analyzer Analyzer
	oldProgram, _, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatal(err)
	}
	newProgram, err := program.Freeze(oldProgram)
	if err != nil {
		t.Fatal(err)
	}
	if len(newProgram.EvidenceIssueNodeIDs) == 0 || len(newProgram.EvidenceIssueTemplateIDs) == 0 {
		t.Fatal("fixture has no evidence issue template")
	}
	templateID := newProgram.EvidenceIssueTemplateIDs[0]
	literalStart := newProgram.TemplateLiteralStarts[templateID-1]
	newProgram.TemplateBytes[literalStart] ^= 1
	batch := resultbatch.Batch{
		Rows: 1, OutcomeIDs: []schema.OutcomeID{4},
		RequirementOffsets: []uint32{0, 0}, DriverOffsets: []uint32{0, 0}, EvidenceOffsets: []uint32{0, 0},
		ReasonOffsets: []uint32{0, 1}, RemediationOffsets: []uint32{0, 0},
		ReasonIDs: []schema.ReasonID{1}, ReasonNodes: []schema.NodeID{newProgram.EvidenceIssueNodeIDs[0]},
		ReasonEvidenceIDs: []schema.EvidenceID{0}, ReasonEvidenceStates: []schema.EvidenceStateID{0},
	}
	if resultRowEqual(oldProgram, &newProgram, &batch, &batch, 0) {
		t.Fatal("used evidence issue template change reported equal")
	}
}
