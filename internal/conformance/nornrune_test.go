package conformance_test

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"github.com/sebishogun/nornrune/internal/adapters/jsonbatch"
	"github.com/sebishogun/nornrune/internal/adapters/jsonpolicy"
	"github.com/sebishogun/nornrune/internal/adapters/jsonresult"
	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/buildinfo"
	"github.com/sebishogun/nornrune/internal/compile"
	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/fixtures"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func TestNornRuneConformance(t *testing.T) {
	fields, symbols, err := nornrune.NewSchema()
	if err != nil {
		t.Fatalf("build policy schema: %v", err)
	}
	policySource := []byte(nornrune.Source())
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

	wantDecisions := []string{"Approve", "Reject", "Revise", "Escalate", "Escalate"}
	wantRequirements := [][]schema.RequirementID{{1, 2}, {1, 2}, {2, 3}, {1, 2}, {2, 3}}
	for row := range int(decisions.Rows) {
		start := decisions.RequirementOffsets[row]
		end := decisions.RequirementOffsets[row+1]
		decision := outcomeName(t, compiled, decisions.OutcomeIDs[row])
		if batch.RequestIDs[row] != schema.RequestID(row+1) || decision != wantDecisions[row] ||
			!slices.Equal(decisions.RequirementIDs[start:end], wantRequirements[row]) {
			t.Fatalf("row %d = R%d %s %v; want R%d %s %v", row, batch.RequestIDs[row], decision,
				decisions.RequirementIDs[start:end], row+1, wantDecisions[row], wantRequirements[row])
		}
	}

	var encoder jsonresult.Encoder
	if err := encoder.Bind(compiled); err != nil {
		t.Fatalf("bind result encoder: %v", err)
	}
	encoded, err := encoder.Append(nil, batch.RequestIDs, &decisions, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("encode results: %v", err)
	}
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
