package diff

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/sebishogun/nornrune/internal/adapters/jsonresult"
	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	resultbatch "github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func TestExhaustiveDecisionOracleCoversAllTransitions(t *testing.T) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatal(err)
	}
	var matrix RiskMatrix
	for old := Approve; old <= Escalate; old++ {
		for next := Approve; next <= Escalate; next++ {
			class := Equivalent
			if old < next {
				class = Widened
			} else if old > next {
				class = Narrowed
			}
			if err := matrix.Set(old, next, Transition{Class: class, Allowed: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	oldResults := resultbatch.Batch{Rows: 16, OutcomeIDs: make([]schema.OutcomeID, 16)}
	newResults := resultbatch.Batch{Rows: 16, OutcomeIDs: make([]schema.OutcomeID, 16)}
	for _, offsets := range []*[]uint32{
		&oldResults.RequirementOffsets, &oldResults.DriverOffsets, &oldResults.EvidenceOffsets, &oldResults.ReasonOffsets, &oldResults.RemediationOffsets,
		&newResults.RequirementOffsets, &newResults.DriverOffsets, &newResults.EvidenceOffsets, &newResults.ReasonOffsets, &newResults.RemediationOffsets,
	} {
		*offsets = make([]uint32, 17)
	}
	row := 0
	for old := Approve; old <= Escalate; old++ {
		for next := Approve; next <= Escalate; next++ {
			oldResults.OutcomeIDs[row] = schema.OutcomeID(old)
			newResults.OutcomeIDs[row] = schema.OutcomeID(next)
			row++
		}
	}
	result := Result{Outcome: Equivalent, HasCounterexample: true}
	if err := compareResultBatch(
		&result, oldProgram, newProgram, nil, nil, &oldResults, &newResults,
		&searchPlan{}, Domain{}, matrix, 0,
	); err != nil {
		t.Fatalf("compare oracle rows: %v", err)
	}
	if result.Outcome != Changed {
		t.Fatalf("mixed transition outcome: %s", result.Outcome)
	}
	for index, count := range result.Transitions {
		if count != 1 {
			t.Fatalf("transition %d count = %d, want 1", index, count)
		}
	}
}

func TestExhaustiveComparisonWitnessStableAcrossBatchTails(t *testing.T) {
	changed := changedPolicySource()
	var want Result
	for _, batchRows := range []uint32{1, 63, 64, 65} {
		domain := comparisonDomain()
		domain.BatchRows = batchRows
		var analyzer Analyzer
		var result Result
		if err := analyzer.Compare(
			context.Background(), &result, []byte(nornrune.Source()), changed,
			nativeFieldSchema(), domain, uniformRiskMatrix(Changed, true), nil,
		); err != nil {
			t.Fatalf("batch rows %d: %v", batchRows, err)
		}
		if batchRows == 1 {
			want = result
			continue
		}
		if result.Outcome != want.Outcome || result.Counterexample.Index != want.Counterexample.Index ||
			result.Counterexample.Old.Decision != want.Counterexample.Old.Decision || result.Counterexample.New.Decision != want.Counterexample.New.Decision {
			t.Fatalf("batch rows %d changed result: got %+v want %+v", batchRows, result, want)
		}
	}
}

func TestAnalyzerAgreesWithDirectEvaluatorOracle(t *testing.T) {
	tests := []struct {
		name             string
		oldSpec, newSpec tinyPolicySpec
		withEvidence     bool
	}{
		{
			name:    "opcode",
			oldSpec: tinyPolicySpec{assertOp: "equal", assertValue: true, falseOutcome: "Reject"},
			newSpec: tinyPolicySpec{assertOp: "not_equal", assertValue: true, falseOutcome: "Reject"},
		},
		{
			name:    "literal value",
			oldSpec: tinyPolicySpec{assertOp: "equal", assertValue: true, falseOutcome: "Reject"},
			newSpec: tinyPolicySpec{assertOp: "equal", assertValue: false, falseOutcome: "Reject"},
		},
		{
			name:    "resolution",
			oldSpec: tinyPolicySpec{assertOp: "equal", assertValue: true, falseOutcome: "Reject"},
			newSpec: tinyPolicySpec{assertOp: "equal", assertValue: true, falseOutcome: "Revise"},
		},
		{
			name:    "remediation",
			oldSpec: tinyPolicySpec{assertOp: "equal", assertValue: true, falseOutcome: "Revise"},
			newSpec: tinyPolicySpec{assertOp: "equal", assertValue: true, falseOutcome: "Revise", remediation: true},
		},
		{
			name:         "evidence state",
			oldSpec:      tinyPolicySpec{assertOp: "equal", assertValue: true, falseOutcome: "Reject", evidenceState: "valid"},
			newSpec:      tinyPolicySpec{assertOp: "equal", assertValue: true, falseOutcome: "Reject", evidenceState: "stale"},
			withEvidence: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldSource := tinyPolicySource(test.oldSpec)
			newSource := tinyPolicySource(test.newSpec)
			for _, batchRows := range []uint32{1, 2, 3} {
				domain := tinyOracleDomain(test.withEvidence, batchRows)
				assertAnalyzerMatchesDirectOracle(t, oldSource, newSource, domain)
				assertAnalyzerMatchesDirectOracle(t, newSource, oldSource, domain)
			}
		})
	}
}

type tinyPolicySpec struct {
	assertOp      string
	evidenceState string
	falseOutcome  string
	assertValue   bool
	remediation   bool
}

func tinyPolicySource(spec tinyPolicySpec) []byte {
	evidence := "[]"
	if spec.evidenceState != "" {
		evidence = fmt.Sprintf(`[{"op":"evidence_matches","kind":"approval_record","state":%q,"explanation":{"issue":"Approval evidence is {reason}.","conflict":"Approval evidence conflicts."}}]`, spec.evidenceState)
	}
	remediations := "[]"
	if spec.remediation {
		remediations = `[{"kind":"add_evidence","evidence_kind":"approval_record"}]`
	}
	return []byte(fmt.Sprintf(`{
  "schema_version":1,
  "name":"tiny-oracle",
  "version":"1.0.0",
  "assumptions":[],
  "evidence_kinds":[{"name":"approval_record"}],
  "evidence_states":[{"name":"valid"},{"name":"stale"},{"name":"conflicting"}],
  "outcomes":[
    {"name":"Approve","precedence":1,"terminal":true},
    {"name":"Reject","precedence":4,"terminal":true},
    {"name":"Revise","precedence":2,"terminal":false},
    {"name":"Escalate","precedence":3,"terminal":true}
  ],
  "requirements":[{
    "id":"R1",
    "source":"A present request flag and any required approval evidence must satisfy this generated policy.",
    "applies":{"op":"exists","field":"request.allowed"},
    "clauses":[{
      "assert":{"op":%q,"field":"request.allowed","value":%t},
      "evidence":%s,
      "resolution":{
        "satisfied":{"outcome":"Approve","explanation":{"rationale":"The generated condition is satisfied.","uncertainty":[]}},
        "false":{"outcome":%q,"explanation":{"rationale":"The generated condition is false.","uncertainty":[]}},
        "missing":{"outcome":"Revise","explanation":{"rationale":"Required generated input is missing.","uncertainty":[]}},
        "stale":{"outcome":"Escalate","explanation":{"rationale":"Required generated evidence is stale.","uncertainty":[]}},
        "unclear":{"outcome":"Escalate","explanation":{"rationale":"Required generated evidence is unclear.","uncertainty":[]}},
        "unverifiable":{"outcome":"Escalate","explanation":{"rationale":"Required generated evidence is unverifiable.","uncertainty":[]}},
        "conflict":{"outcome":"Escalate","explanation":{"rationale":"Required generated evidence conflicts.","uncertainty":[]}}
      },
      "remediations":%s
    }]
  }]
}`, spec.assertOp, spec.assertValue, evidence, spec.falseOutcome, remediations))
}

func tinyOracleDomain(withEvidence bool, batchRows uint32) Domain {
	domain := Domain{
		Fields: []FieldDomain{{
			Name: "request.allowed",
			Kind: FieldKindBoolean,
			Values: []Value{
				{State: ValueMissing, Kind: FieldKindBoolean},
				{State: ValuePresent, Kind: FieldKindBoolean},
				{State: ValuePresent, Kind: FieldKindBoolean, Boolean: true},
			},
			Closed: true,
		}},
		MaxCandidates: 3,
		BatchRows:     batchRows,
	}
	if withEvidence {
		domain.EvidenceSets = []EvidenceSet{
			{},
			{Records: []Evidence{{Kind: "approval_record", State: "valid"}}},
			{Records: []Evidence{{Kind: "approval_record", State: "stale"}}},
			{Records: []Evidence{
				{Kind: "approval_record", State: "valid"},
				{Kind: "approval_record", State: "stale"},
			}},
		}
		domain.EvidenceClosed = true
		domain.MaxCandidates *= uint64(len(domain.EvidenceSets))
	}
	return domain
}

func assertAnalyzerMatchesDirectOracle(t *testing.T, oldSource, newSource []byte, domain Domain) {
	t.Helper()
	fields := FieldSchema{Fields: []FieldSpec{
		{Name: "request.allowed", Kind: FieldKindBoolean, Group: FieldGroupContext},
	}}
	matrix := uniformRiskMatrix(Changed, true)
	var compiler Analyzer
	oldProgram, newProgram, err := compiler.compilePair(oldSource, newSource, fields)
	if err != nil {
		t.Fatalf("compilePair() error = %v", err)
	}
	want := directOracleResult(t, oldProgram, newProgram, domain, matrix)
	var analyzer Analyzer
	var got Result
	if err := analyzer.Compare(context.Background(), &got, oldSource, newSource, fields, domain, matrix, nil); err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if got.Outcome != want.Outcome || got.Complete != want.Complete || got.Candidates != want.Candidates ||
		got.Transitions != want.Transitions || got.HasCounterexample != want.HasCounterexample {
		t.Fatalf("analyzer = %+v, direct oracle = %+v", got, want)
	}
	if want.HasCounterexample && (got.Counterexample.Index != want.Counterexample.Index ||
		got.Counterexample.Old.Decision != want.Counterexample.Old.Decision ||
		got.Counterexample.New.Decision != want.Counterexample.New.Decision) {
		t.Fatalf("analyzer witness = %+v, direct oracle witness = %+v", got.Counterexample, want.Counterexample)
	}
}

func directOracleResult(
	t *testing.T,
	oldProgram, newProgram *program.Program,
	domain Domain,
	matrix RiskMatrix,
) Result {
	t.Helper()
	want := Result{Outcome: Equivalent, Complete: true}
	evidenceSets := domain.EvidenceSets
	if len(evidenceSets) == 0 {
		evidenceSets = []EvidenceSet{{}}
	}
	index := uint64(0)
	for _, evidence := range evidenceSets {
		for _, value := range domain.Fields[0].Values {
			oldDecision, oldJSON := directEvaluate(t, oldProgram, value, evidence)
			newDecision, newJSON := directEvaluate(t, newProgram, value, evidence)
			transition, ok := transitionIndex(oldDecision, newDecision)
			if !ok {
				t.Fatalf("invalid direct decisions %v -> %v", oldDecision, newDecision)
			}
			want.Transitions[transition]++
			if bytes.Equal(oldJSON, newJSON) {
				index++
				continue
			}
			class := Changed
			if oldDecision != newDecision {
				entry, found := matrix.Lookup(oldDecision, newDecision)
				if !found {
					t.Fatalf("missing transition %v -> %v", oldDecision, newDecision)
				}
				class = entry.Class
			}
			want.Outcome = mergeOracleOutcome(want.Outcome, class)
			if !want.HasCounterexample {
				want.Counterexample.Index = index
				want.Counterexample.Old.Decision = oldDecision
				want.Counterexample.New.Decision = newDecision
				want.HasCounterexample = true
			}
			index++
		}
	}
	want.Candidates = index
	return want
}

func mergeOracleOutcome(current, next Outcome) Outcome {
	if current == Equivalent {
		return next
	}
	if current == next || next == Equivalent {
		return current
	}
	return Changed
}

func directEvaluate(t *testing.T, compiled *program.Program, value Value, evidence EvidenceSet) (Decision, []byte) {
	t.Helper()
	records := make([]eval.EvidenceRecord, 0, len(evidence.Records))
	for _, source := range evidence.Records {
		kind, kindOK := directCatalogID(compiled, compiled.EvidenceKindNames, source.Kind)
		state, stateOK := directCatalogID(compiled, compiled.EvidenceStateNames, source.State)
		if !kindOK || !stateOK {
			continue
		}
		records = append(records, eval.EvidenceRecord{
			ID: schema.EvidenceID(len(records) + 1), Kind: schema.EvidenceKindID(kind), State: schema.EvidenceStateID(state),
		})
	}
	var builder eval.Builder
	if err := builder.Begin(compiled, 1, uint32(len(records)), uint32(len(records))); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	if value.State == ValuePresent {
		if err := builder.SetBoolean(0, 1, value.Boolean); err != nil {
			t.Fatal(err)
		}
	}
	refs := make([]uint32, len(records))
	for row, record := range records {
		if err := builder.SetEvidence(uint32(row), record); err != nil {
			t.Fatal(err)
		}
		refs[row] = uint32(row)
	}
	if err := builder.SetEvidenceCSR([]uint32{0, uint32(len(records))}, refs); err != nil {
		t.Fatal(err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var executor eval.Executor
	var results resultbatch.Batch
	if err := executor.Execute(&results, compiled, batch); err != nil {
		t.Fatal(err)
	}
	decision := Decision(results.OutcomeIDs[0])
	if !decision.Valid() {
		t.Fatalf("invalid direct decision %v", decision)
	}
	var encoder jsonresult.Encoder
	if err := encoder.Bind(compiled); err != nil {
		t.Fatal(err)
	}
	encoded, err := encoder.Append(nil, []schema.RequestID{1}, &results, []byte("direct-oracle"))
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(encoded, []byte("\"results\": ["))
	if start < 0 {
		t.Fatalf("result JSON missing rows: %s", encoded)
	}
	return decision, encoded[start:]
}

func directCatalogID(compiled *program.Program, names []schema.SymbolID, value string) (uint32, bool) {
	for row, name := range names {
		candidate, ok := compiled.Symbol(name)
		if ok && bytes.Equal(candidate, []byte(value)) {
			return uint32(row + 1), true
		}
	}
	return 0, false
}
