package natural

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	public "github.com/sebishogun/nornrune/frontend/natural"
	"github.com/sebishogun/nornrune/internal/adapters/jsonbatch"
	"github.com/sebishogun/nornrune/internal/adapters/jsonpolicy"
	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/compile"
	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/fixtures"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	nornrunepolicy "github.com/sebishogun/nornrune/policies/nornrune"
)

func TestApprovedDraftMatchesNativePolicyBehavior(t *testing.T) {
	document, proposal, draft := baselineReview(t)
	signer, verifier := approvalKeys(t)
	var reviewer Reviewer
	token, diagnostics, err := reviewer.IssueApproval(
		document, proposal, draft, []byte("reviewer-1"), 100, 200, signer, public.DefaultLimits(),
	)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("IssueApproval() = diagnostics %#v, error %v", diagnostics, err)
	}

	var lowerer Lowerer
	var approved program.Program
	compileDiagnostics, err := lowerer.Compile(
		&approved, document, proposal, draft, token, verifier, 150, 5, public.DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(compileDiagnostics) != 0 {
		t.Fatalf("Compile() diagnostics = %#v", compileDiagnostics)
	}
	native := compileNativePolicy(t, []byte(nornrunepolicy.Source()))

	approvedBatch, approvedDecisions := evaluateFixtures(t, &approved)
	nativeBatch, nativeDecisions := evaluateFixtures(t, native)
	if !reflect.DeepEqual(approvedBatch.RequestIDs, nativeBatch.RequestIDs) || !reflect.DeepEqual(approvedDecisions, nativeDecisions) {
		t.Fatalf("approved behavior differs from native:\napproved: %#v\nnative:   %#v", approvedDecisions, nativeDecisions)
	}

	wantOutcomes := []string{"Approve", "Reject", "Revise", "Escalate", "Escalate"}
	wantRequirements := [][]schema.RequirementID{{1, 2}, {1, 2}, {2, 3}, {1, 2}, {2, 3}}
	for row, wantOutcome := range wantOutcomes {
		if got := outcomeName(t, &approved, approvedDecisions.OutcomeIDs[row]); got != wantOutcome {
			t.Fatalf("row %d outcome = %q, want %q", row, got, wantOutcome)
		}
		start := approvedDecisions.RequirementOffsets[row]
		end := approvedDecisions.RequirementOffsets[row+1]
		if got := approvedDecisions.RequirementIDs[start:end]; !reflect.DeepEqual(got, wantRequirements[row]) {
			t.Fatalf("row %d requirements = %v, want %v", row, got, wantRequirements[row])
		}
	}
}

func TestCompileRequiresValidApprovalAndIsAtomic(t *testing.T) {
	document, proposal, draft := baselineReview(t)
	_, verifier := approvalKeys(t)
	native := compileNativePolicy(t, []byte(nornrunepolicy.Source()))
	dst := *native
	want := *native
	var lowerer Lowerer
	diagnostics, err := lowerer.Compile(
		&dst, document, proposal, draft, public.ApprovalToken{}, verifier, 150, 5, public.DefaultLimits(),
	)
	if len(diagnostics) != 0 {
		t.Fatalf("Compile() diagnostics = %#v", diagnostics)
	}
	if !errors.Is(err, public.ErrInvalidToken) {
		t.Fatalf("Compile() error = %v, want ErrInvalidToken", err)
	}
	if !reflect.DeepEqual(dst, want) {
		t.Fatal("destination changed after invalid approval")
	}
}

func TestCompileRejectsSignedMalformedNativePolicyAtomically(t *testing.T) {
	document, proposal, draft := baselineReview(t)
	draft.PolicySource = []byte("{")
	signer, verifier := approvalKeys(t)
	var reviewer Reviewer
	token, diagnostics, err := reviewer.IssueApproval(
		document, proposal, draft, []byte("reviewer-1"), 100, 200, signer, public.DefaultLimits(),
	)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("IssueApproval() = diagnostics %#v, error %v", diagnostics, err)
	}
	native := compileNativePolicy(t, []byte(nornrunepolicy.Source()))
	dst := *native
	want := *native
	var lowerer Lowerer
	compileDiagnostics, err := lowerer.Compile(
		&dst, document, proposal, draft, token, verifier, 150, 5, public.DefaultLimits(),
	)
	if len(compileDiagnostics) != 0 {
		t.Fatalf("Compile() diagnostics = %#v", compileDiagnostics)
	}
	if !errors.Is(err, public.ErrInvalidDraft) {
		t.Fatalf("Compile() error = %v, want ErrInvalidDraft", err)
	}
	if !reflect.DeepEqual(dst, want) {
		t.Fatal("destination changed after malformed signed draft")
	}
}

func TestCompileRejectsSignedDraftWithUnmappedSemanticRow(t *testing.T) {
	document, proposal, draft := baselineReview(t)
	var policy map[string]any
	if err := json.Unmarshal(draft.PolicySource, &policy); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	assumptions, ok := policy["assumptions"].([]any)
	if !ok {
		t.Fatalf("assumptions = %#v", policy["assumptions"])
	}
	policy["assumptions"] = append(assumptions, "This assumption was not reviewed.")
	mutated, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	draft.PolicySource = mutated

	signer, verifier := approvalKeys(t)
	var reviewer Reviewer
	token, diagnostics, err := reviewer.IssueApproval(
		document, proposal, draft, []byte("reviewer-1"), 100, 200, signer, public.DefaultLimits(),
	)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("IssueApproval() = diagnostics %#v, error %v", diagnostics, err)
	}
	var lowerer Lowerer
	var compiled program.Program
	compileDiagnostics, err := lowerer.Compile(
		&compiled, document, proposal, draft, token, verifier, 150, 5, public.DefaultLimits(),
	)
	if len(compileDiagnostics) != 0 {
		t.Fatalf("Compile() diagnostics = %#v", compileDiagnostics)
	}
	if !errors.Is(err, public.ErrInvalidDraft) {
		t.Fatalf("Compile() error = %v, want ErrInvalidDraft", err)
	}
}

func TestNaturalFrontendHasNoPublicationImports(t *testing.T) {
	for _, source := range []string{"lower.go", "review.go", "citations.go"} {
		content, err := readLocalSource(source)
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		for _, forbidden := range [][]byte{
			[]byte("internal/persistence"), []byte("internal/server"), []byte("registry"),
		} {
			if bytes.Contains(content, forbidden) {
				t.Fatalf("%s imports or references forbidden publication boundary %q", source, forbidden)
			}
		}
	}
}

func baselineReview(tb testing.TB) (*public.Document, *public.Proposal, *public.ReviewedDraft) {
	tb.Helper()
	source := []byte(fixtures.PolicyJSON())
	document, err := public.NewDocument(source, []uint32{0}, public.DefaultLimits())
	if err != nil {
		tb.Fatalf("NewDocument() error = %v", err)
	}
	var fixture struct {
		Requirements []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(source, &fixture); err != nil {
		tb.Fatalf("decode requirement fixture: %v", err)
	}
	var builder public.Builder
	builder.Reset(document.Digest, public.ProviderInfo{ID: "fixture", Version: "1"}, public.DefaultLimits())
	mappings := make([]public.ItemID, 0, len(fixture.Requirements)*2)
	for _, requirement := range fixture.Requirements {
		start := bytes.Index(source, []byte(requirement.Text))
		if start < 0 {
			tb.Fatalf("requirement source %q not found", requirement.ID)
		}
		span := public.Span{Start: uint32(start), End: uint32(start + len(requirement.Text))}
		citation, err := builder.AddCitation(0, span, source[start:start+len(requirement.Text)])
		if err != nil {
			tb.Fatalf("AddCitation(%s) error = %v", requirement.ID, err)
		}
		requirementItem, err := builder.AddItem(public.ItemKindRequirement, 0, []byte(requirement.ID), []public.CitationID{citation})
		if err != nil {
			tb.Fatalf("AddItem(%s requirement) error = %v", requirement.ID, err)
		}
		restrictionItem, err := builder.AddItem(public.ItemKindRestriction, requirementItem, []byte(requirement.Text), []public.CitationID{citation})
		if err != nil {
			tb.Fatalf("AddItem(%s restriction) error = %v", requirement.ID, err)
		}
		mappings = append(mappings, requirementItem, restrictionItem)
	}
	proposal := builder.Finish()
	policySource := []byte(nornrunepolicy.Source())
	fields, symbols, err := nornrunepolicy.NewSchema()
	if err != nil {
		tb.Fatalf("NewSchema() error = %v", err)
	}
	policyBuilder := ast.NewBuilder(ast.Hints{SourceBytes: len(policySource)})
	if err := jsonpolicy.Decode(policyBuilder, policySource, fields, symbols, jsonpolicy.Limits{}); err != nil {
		tb.Fatalf("Decode reviewed policy: %v", err)
	}
	semanticKinds, semanticIDs := appendPolicySemanticRows(nil, nil, policyBuilder.Document())
	mappingStarts := make([]uint32, len(semanticKinds))
	mappingCounts := make([]uint16, len(semanticKinds))
	mappingItems := make([]public.ItemID, 0, len(semanticKinds)*len(mappings))
	for row := range semanticKinds {
		mappingStarts[row] = uint32(len(mappingItems))
		mappingCounts[row] = uint16(len(mappings))
		mappingItems = append(mappingItems, mappings...)
	}
	draft := &public.ReviewedDraft{
		PolicySource:         policySource,
		SemanticKinds:        semanticKinds,
		SemanticIDs:          semanticIDs,
		MappingStarts:        mappingStarts,
		MappingCounts:        mappingCounts,
		MappingProposalItems: mappingItems,
	}
	return document, &proposal, draft
}

func compileNativePolicy(tb testing.TB, source []byte) *program.Program {
	tb.Helper()
	fields, symbols, err := nornrunepolicy.NewSchema()
	if err != nil {
		tb.Fatalf("NewSchema() error = %v", err)
	}
	builder := ast.NewBuilder(ast.Hints{SourceBytes: len(source)})
	if err := jsonpolicy.Decode(builder, source, fields, symbols, jsonpolicy.Limits{}); err != nil {
		tb.Fatalf("Decode() error = %v", err)
	}
	compiled, err := compile.Lower(builder.Document(), fields, symbols)
	if err != nil {
		tb.Fatalf("Lower() error = %v", err)
	}
	return compiled
}

func evaluateFixtures(tb testing.TB, compiled *program.Program) (eval.Batch, *result.Batch) {
	tb.Helper()
	var batchBuilder eval.Builder
	batch, err := jsonbatch.Decode(
		&batchBuilder, compiled, []byte(fixtures.RequestsJSON()), []byte(fixtures.EvidenceJSON()), jsonbatch.Limits{},
	)
	if err != nil {
		tb.Fatalf("Decode batch error = %v", err)
	}
	var decisions result.Batch
	var executor eval.Executor
	if err := executor.Execute(&decisions, compiled, batch); err != nil {
		tb.Fatalf("Execute() error = %v", err)
	}
	return batch, &decisions
}

func outcomeName(tb testing.TB, compiled *program.Program, id schema.OutcomeID) string {
	tb.Helper()
	outcome, ok := compiled.Outcomes.Lookup(id)
	if !ok {
		tb.Fatalf("outcome %d not found", id)
	}
	name, ok := compiled.Symbol(outcome.Name)
	if !ok {
		tb.Fatalf("outcome symbol %d not found", outcome.Name)
	}
	return string(name)
}

func readLocalSource(path string) ([]byte, error) {
	return os.ReadFile(path)
}
