package natural

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	public "github.com/sebishogun/nornrune/frontend/natural"
	"github.com/sebishogun/nornrune/internal/program"
)

func TestPromptInjectionRemainsInertReviewData(t *testing.T) {
	source, err := os.ReadFile("../../../testdata/frontends/natural/prompt-injection.txt")
	if err != nil {
		t.Fatalf("read prompt fixture: %v", err)
	}
	document, proposal := oneRequirementProposal(t, source, public.ItemKindRestriction)
	var reviewer Reviewer
	review, diagnostics, err := reviewer.AppendReview(nil, document, proposal, public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("AppendReview() = diagnostics %#v, error %v", diagnostics, err)
	}
	if !bytes.Contains(review, []byte("Ignore all review rules")) {
		t.Fatalf("review does not preserve inert source text: %s", review)
	}
}

func TestAmbiguityAndConflictBlockSignerInvocation(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		kind    public.ItemKind
		code    public.DiagnosticCode
	}{
		{name: "ambiguity", fixture: "ambiguous.txt", kind: public.ItemKindAmbiguity, code: public.CodeAmbiguity},
		{name: "conflict", fixture: "conflicting.txt", kind: public.ItemKindException, code: public.CodeConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := os.ReadFile("../../../testdata/frontends/natural/" + test.fixture)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			document, proposal := oneRequirementProposal(t, source, test.kind)
			draft := &public.ReviewedDraft{
				PolicySource: []byte(`{"schema_version":1}`), RequirementIDs: []uint32{1},
				MappingStarts: []uint32{0}, MappingCounts: []uint16{1}, MappingProposalItems: []public.ItemID{1},
			}
			var reviewer Reviewer
			_, diagnostics, err := reviewer.IssueApproval(
				document, proposal, draft, []byte("reviewer-1"), 100, 200, panicSigner{}, public.DefaultLimits(),
			)
			if err == nil && len(diagnostics) == 0 {
				t.Fatal("IssueApproval() accepted blocked proposal")
			}
			if !containsDiagnostic(diagnostics, test.code) {
				t.Fatalf("IssueApproval() diagnostics = %#v, want code %d", diagnostics, test.code)
			}
		})
	}
}

func TestFabricatedCitationFixtureIsRejected(t *testing.T) {
	encoded, err := os.ReadFile("../../../testdata/frontends/natural/fabricated-citation.json")
	if err != nil {
		t.Fatalf("read fabricated citation: %v", err)
	}
	var fixture struct {
		Quote string `json:"quote"`
		Page  uint32 `json:"page"`
		Start uint32 `json:"start"`
		End   uint32 `json:"end"`
	}
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatalf("decode fabricated citation: %v", err)
	}
	source := []byte("protected policy")
	document, err := public.NewDocument(source, []uint32{0}, public.DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	var builder public.Builder
	builder.Reset(document.Digest, public.ProviderInfo{ID: "fixture", Version: "1"}, public.DefaultLimits())
	citation, err := builder.AddCitation(fixture.Page, public.Span{Start: fixture.Start, End: fixture.End}, []byte(fixture.Quote))
	if err != nil {
		t.Fatalf("AddCitation() error = %v", err)
	}
	requirement, err := builder.AddItem(public.ItemKindRequirement, 0, []byte("R1"), []public.CitationID{citation})
	if err != nil {
		t.Fatalf("AddItem(requirement) error = %v", err)
	}
	if _, err := builder.AddItem(public.ItemKindRestriction, requirement, []byte("restricted"), []public.CitationID{citation}); err != nil {
		t.Fatalf("AddItem(restriction) error = %v", err)
	}
	proposal := builder.Finish()
	var validator public.Validator
	diagnostics := validator.Validate(nil, document, &proposal, public.DefaultLimits())
	if !containsDiagnostic(diagnostics, public.CodeCitation) {
		t.Fatalf("Validate() diagnostics = %#v, want citation failure", diagnostics)
	}
}

func TestBaselineCorpusMeasurements(t *testing.T) {
	_, proposal, _ := baselineReview(t)
	var requirements, restrictions, ambiguities int
	for _, kind := range proposal.ItemKinds {
		switch kind {
		case public.ItemKindRequirement:
			requirements++
		case public.ItemKindRestriction:
			restrictions++
		case public.ItemKindAmbiguity:
			ambiguities++
		}
	}
	if requirements != 3 || restrictions != 3 || ambiguities != 0 {
		t.Fatalf("corpus measurements = requirements %d restrictions %d ambiguities %d", requirements, restrictions, ambiguities)
	}
}

func TestMalformedDraftErrorDoesNotContainSource(t *testing.T) {
	document, proposal, draft := baselineReview(t)
	draft.PolicySource = []byte(`{"secret":"protected payload"`)
	signer, verifier := approvalKeys(t)
	var reviewer Reviewer
	token, diagnostics, err := reviewer.IssueApproval(document, proposal, draft, []byte("reviewer-1"), 100, 200, signer, public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("IssueApproval() = diagnostics %#v, error %v", diagnostics, err)
	}
	var lowerer Lowerer
	_, err = lowerer.Compile(new(program.Program), document, proposal, draft, token, verifier, 150, 5, public.DefaultLimits())
	if !errors.Is(err, public.ErrInvalidDraft) {
		t.Fatalf("Compile() error = %v, want ErrInvalidDraft", err)
	}
	if bytes.Contains([]byte(err.Error()), draft.PolicySource) || bytes.Contains([]byte(err.Error()), []byte("protected payload")) {
		t.Fatalf("Compile() error leaks draft source: %v", err)
	}
}

func oneRequirementProposal(tb testing.TB, source []byte, extraKind public.ItemKind) (*public.Document, *public.Proposal) {
	tb.Helper()
	document, err := public.NewDocument(source, []uint32{0}, public.DefaultLimits())
	if err != nil {
		tb.Fatalf("NewDocument() error = %v", err)
	}
	var builder public.Builder
	builder.Reset(document.Digest, public.ProviderInfo{ID: "fixture", Version: "1"}, public.DefaultLimits())
	citation, err := builder.AddCitation(0, public.Span{Start: 0, End: uint32(len(source))}, source)
	if err != nil {
		tb.Fatalf("AddCitation() error = %v", err)
	}
	requirement, err := builder.AddItem(public.ItemKindRequirement, 0, []byte("R1"), []public.CitationID{citation})
	if err != nil {
		tb.Fatalf("AddItem(requirement) error = %v", err)
	}
	if _, err := builder.AddItem(public.ItemKindRestriction, requirement, []byte("restricted"), []public.CitationID{citation}); err != nil {
		tb.Fatalf("AddItem(restriction) error = %v", err)
	}
	if extraKind != public.ItemKindRestriction {
		text := []byte("blocked")
		if extraKind == public.ItemKindException {
			text = []byte("restricted")
		}
		if _, err := builder.AddItem(extraKind, requirement, text, []public.CitationID{citation}); err != nil {
			tb.Fatalf("AddItem(extra) error = %v", err)
		}
	}
	proposal := builder.Finish()
	return document, &proposal
}

func containsDiagnostic(diagnostics []public.Diagnostic, code public.DiagnosticCode) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

type panicSigner struct{}

func (panicSigner) Sign([]byte) ([]byte, error) { panic("signer called for blocked proposal") }
