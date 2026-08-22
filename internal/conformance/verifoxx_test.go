package conformance_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonbatch"
	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/buildinfo"
	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/fixtures"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

type policyOutput struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type driverOutput struct {
	RequirementID string `json:"requirement_id"`
	ClauseID      string `json:"clause_id"`
	Reason        string `json:"reason"`
}

type remediationOutput struct {
	Action       string `json:"action"`
	EvidenceKind string `json:"evidence_kind"`
}

type decisionOutput struct {
	RequestID                    string              `json:"request_id"`
	Decision                     string              `json:"decision"`
	Rationale                    string              `json:"rationale"`
	Driver                       driverOutput        `json:"driver"`
	RequirementsApplied          []string            `json:"requirements_applied"`
	EvidenceUsed                 []string            `json:"evidence_used"`
	MissingOrConflictingEvidence []string            `json:"missing_or_conflicting_evidence"`
	Assumptions                  []string            `json:"assumptions"`
	UnresolvedUncertainty        []string            `json:"unresolved_uncertainty"`
	Remediation                  []remediationOutput `json:"remediation"`
}

type resultOutput struct {
	SchemaVersion int              `json:"schema_version"`
	Policy        policyOutput     `json:"policy"`
	EngineVersion string           `json:"engine_version"`
	Results       []decisionOutput `json:"results"`
}

func TestVerifoxxConformance(t *testing.T) {
	fields, symbols := conformanceSchema(t)
	policySource, err := os.ReadFile("../../policies/verifoxx/policy.json")
	if err != nil {
		t.Fatal(err)
	}
	builder := ast.NewBuilder(ast.Hints{
		Nodes: 48, CompareNodes: 32, GroupNodes: 12, ChildEdges: 48, EvidenceNodes: 8,
		Values: 96, SymbolValues: 96, SymbolBytes: 4096, EvidenceKinds: 8, EvidenceStates: 16,
		Outcomes: 8, Remediations: 4, Clauses: 8, ClauseEvidenceEdges: 8,
		ClauseRemediationEdges: 4, Requirements: 4, RequirementClauseEdges: 8, SourceBytes: len(policySource),
	})
	if err := jsonpolicy.Decode(builder, policySource, fields, symbols, jsonpolicy.Limits{}); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	compiled, err := compile.Lower(builder.Document(), fields, symbols)
	if err != nil {
		t.Fatalf("compile policy: %v", err)
	}
	requireCompiledQualifiers(t, compiled)

	var batchBuilder eval.Builder
	batch, err := jsonbatch.Decode(&batchBuilder, compiled, []byte(fixtures.RequestsJSON()), []byte(fixtures.EvidenceJSON()), jsonbatch.Limits{})
	if err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	var decisions result.Batch
	var executor eval.Executor
	if err := executor.Execute(&decisions, compiled, batch); err != nil {
		t.Fatalf("execute policy: %v", err)
	}

	got := projectResults(t, compiled, batch, &decisions)
	wantDecisions := []string{"Approve", "Reject", "Revise", "Escalate", "Escalate"}
	wantRequirements := [][]string{{"R1", "R2"}, {"R1", "R2"}, {"R2", "R3"}, {"R1", "R2"}, {"R2", "R3"}}
	for row := range got.Results {
		wantID := fmt.Sprintf("R%d", row+1)
		if got.Results[row].RequestID != wantID || got.Results[row].Decision != wantDecisions[row] ||
			!slices.Equal(got.Results[row].RequirementsApplied, wantRequirements[row]) {
			t.Fatalf("row %d = %s %s %v; want %s %s %v", row, got.Results[row].RequestID,
				got.Results[row].Decision, got.Results[row].RequirementsApplied, wantID, wantDecisions[row], wantRequirements[row])
		}
	}

	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	for _, path := range []string{"../../results/requests.json", "../../testdata/golden/requests.json"} {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encoded, want) {
			t.Fatalf("%s mismatch\n--- got ---\n%s", path, encoded)
		}
	}
}

func conformanceSchema(t *testing.T) (*schema.Schema, *schema.Interner) {
	t.Helper()
	symbols := schema.NewSymbolInterner(16)
	builder := schema.NewBuilder()
	fields := []struct {
		name  string
		group schema.FieldGroup
	}{
		{"requester.team", schema.FieldGroupSubject},
		{"requester.trust", schema.FieldGroupSubject},
		{"action.type", schema.FieldGroupAction},
		{"action.output", schema.FieldGroupOutput},
		{"action.dataset", schema.FieldGroupResource},
		{"environment.execution_env", schema.FieldGroupContext},
		{"environment.usage", schema.FieldGroupContext},
	}
	for _, field := range fields {
		name, err := symbols.Intern([]byte(field.name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := builder.AddField(name, schema.ValueKindSymbol, field.group); err != nil {
			t.Fatal(err)
		}
	}
	return builder.Finish(), symbols
}

func requireCompiledQualifiers(t *testing.T, p *program.Program) {
	t.Helper()
	var local, usage, scope, timing bool
	visit := func(id schema.SymbolID) {
		if id == 0 {
			return
		}
		value, ok := p.Symbol(id)
		if !ok {
			t.Fatalf("invalid qualifier symbol %d", id)
		}
		switch string(value) {
		case "local_approved_env":
			local = true
		case "above_standard_limit":
			usage = true
		case "trusted_internal_only":
			scope = true
		case "before_execution":
			timing = true
		}
	}
	for row, opcode := range p.Opcodes {
		if opcode == program.OpcodeEvidence {
			visit(p.EvidenceSubjects[row])
			visit(p.EvidenceScopes[row])
			visit(p.EvidenceTimings[row])
		}
	}
	if !local || !usage || !scope || !timing {
		t.Fatalf("compiled qualifiers local=%v usage=%v scope=%v timing=%v", local, usage, scope, timing)
	}
}

func projectResults(t *testing.T, p *program.Program, batch eval.Batch, decisions *result.Batch) resultOutput {
	t.Helper()
	name, ok := p.Symbol(p.PolicyName)
	if !ok {
		t.Fatal("invalid policy name")
	}
	version, ok := p.Symbol(p.PolicyVersion)
	if !ok {
		t.Fatal("invalid policy version")
	}
	out := resultOutput{
		SchemaVersion: 1,
		Policy:        policyOutput{Name: string(name), Version: string(version), SHA256: hex.EncodeToString(p.ContentHash[:])},
		EngineVersion: buildinfo.Version(),
		Results:       make([]decisionOutput, batch.Rows),
	}
	for row := uint32(0); row < batch.Rows; row++ {
		out.Results[row] = projectRow(t, p, batch, decisions, row)
	}
	return out
}

func projectRow(t *testing.T, p *program.Program, batch eval.Batch, decisions *result.Batch, row uint32) decisionOutput {
	t.Helper()
	decision := outcomeName(t, p, decisions.OutcomeIDs[row])
	driverStart, driverEnd := decisions.DriverOffsets[row], decisions.DriverOffsets[row+1]
	if driverEnd-driverStart != 1 {
		t.Fatalf("row %d has %d drivers", row, driverEnd-driverStart)
	}
	driverRow := driverStart
	driverRequirement := decisions.DriverRequirements[driverRow]
	driverReason := decisions.DriverReasons[driverRow]
	rationale, missing, uncertainty := semanticDetails(t, decision, driverRequirement, driverReason)

	out := decisionOutput{
		RequestID: fmt.Sprintf("R%d", batch.RequestIDs[row]),
		Decision:  decision,
		Rationale: rationale,
		Driver: driverOutput{
			RequirementID: fmt.Sprintf("R%d", driverRequirement),
			ClauseID:      fmt.Sprintf("C%d", decisions.DriverClauses[driverRow]),
			Reason:        reasonName(driverReason, decision),
		},
		RequirementsApplied:          make([]string, 0, decisions.RequirementOffsets[row+1]-decisions.RequirementOffsets[row]),
		EvidenceUsed:                 make([]string, 0, batch.EvidenceOffsets[row+1]-batch.EvidenceOffsets[row]),
		MissingOrConflictingEvidence: missing,
		Assumptions: []string{
			"The supplied structured fields faithfully represent the underlying request and evidence records.",
		},
		UnresolvedUncertainty: uncertainty,
		Remediation:           make([]remediationOutput, 0, decisions.RemediationOffsets[row+1]-decisions.RemediationOffsets[row]),
	}
	for _, id := range decisions.RequirementIDs[decisions.RequirementOffsets[row]:decisions.RequirementOffsets[row+1]] {
		out.RequirementsApplied = append(out.RequirementsApplied, fmt.Sprintf("R%d", id))
	}
	for _, evidenceRow := range batch.EvidenceRefs[batch.EvidenceOffsets[row]:batch.EvidenceOffsets[row+1]] {
		out.EvidenceUsed = append(out.EvidenceUsed, fmt.Sprintf("E%d", batch.Evidence.IDs[evidenceRow]))
	}
	for _, id := range decisions.RemediationIDs[decisions.RemediationOffsets[row]:decisions.RemediationOffsets[row+1]] {
		remediation, ok := p.Remediations.Lookup(id)
		if !ok || remediation.Kind != result.RemediationAddEvidence || remediation.EvidenceKind == 0 {
			t.Fatalf("row %d has unsupported remediation %d", row, id)
		}
		kind := p.EvidenceKindNames[remediation.EvidenceKind-1]
		name, ok := p.Symbol(kind)
		if !ok {
			t.Fatalf("row %d has invalid remediation kind", row)
		}
		out.Remediation = append(out.Remediation, remediationOutput{Action: "add_evidence", EvidenceKind: string(name)})
	}
	return out
}

func outcomeName(t *testing.T, p *program.Program, id schema.OutcomeID) string {
	t.Helper()
	outcome, ok := p.Outcomes.Lookup(id)
	if !ok {
		t.Fatalf("invalid outcome %d", id)
	}
	name, ok := p.Symbol(outcome.Name)
	if !ok {
		t.Fatalf("invalid outcome name %d", outcome.Name)
	}
	return string(name)
}

func reasonName(reason schema.ReasonID, decision string) string {
	switch reason {
	case truth.ReasonMissing:
		return "missing"
	case truth.ReasonStale:
		return "stale"
	case truth.ReasonUnclear:
		return "unclear"
	case truth.ReasonUnverifiable:
		return "unverifiable"
	case truth.ReasonWrongScope:
		return "wrong_scope"
	case truth.ReasonWrongSubject:
		return "wrong_subject"
	case truth.ReasonWrongTiming:
		return "wrong_timing"
	case truth.ReasonInvalid:
		return "invalid"
	case truth.ReasonConflict:
		return "conflict"
	}
	if decision == "Approve" {
		return "satisfied"
	}
	return "condition_false"
}

func semanticDetails(t *testing.T, decision string, requirement schema.RequirementID, reason schema.ReasonID) (string, []string, []string) {
	t.Helper()
	empty := []string{}
	switch {
	case decision == "Approve" && requirement == 1:
		return "The external aggregate request has valid pre-execution approval and verified approved-local-environment evidence.", empty, empty
	case decision == "Reject" && requirement == 1:
		return "The requested individual-record export violates R1's non-negotiable disclosure restriction.", empty, empty
	case decision == "Revise" && requirement == 3 && reason == truth.ReasonMissing:
		return "The above-standard usage request can be corrected by providing the required scoped usage-adjustment approval.",
			[]string{"E3 usage_limit_adjustment is missing from the request."},
			[]string{"Whether a qualifying usage adjustment will be approved remains unresolved."}
	case decision == "Escalate" && requirement == 2 && reason == truth.ReasonMissing:
		return "The approved local execution environment cannot be verified because the required attestation is missing.",
			[]string{"E2 execution_environment_attestation is missing from the request."},
			[]string{"The request's execution environment lacks a verified approved-local attestation."}
	case decision == "Escalate" && requirement == 3 && reason == truth.ReasonConflict:
		return "The pre-execution approval record is conflicting, so the request cannot be decided automatically.",
			[]string{"E4 approval_record has conflicting approval state."},
			[]string{"The valid pre-execution approval state cannot be established from conflicting evidence."}
	default:
		t.Fatalf("unsupported semantic result %s R%d reason=%d", decision, requirement, reason)
		return "", nil, nil
	}
}
