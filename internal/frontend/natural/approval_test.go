package natural

import (
	"crypto/ed25519"
	"errors"
	"math"
	"testing"

	public "github.com/sebishogun/nornrune/frontend/natural"
)

func TestReviewedDraftDigestAcceptsCompleteProvenance(t *testing.T) {
	document, proposal := reviewProposal(t)
	draft := validDraft()
	var reviewer Reviewer
	first, diagnostics, err := reviewer.DraftDigest(document, proposal, draft, public.DefaultLimits())
	if err != nil {
		t.Fatalf("DraftDigest() error = %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("DraftDigest() diagnostics = %#v", diagnostics)
	}
	draft.PolicySource = append([]byte(nil), draft.PolicySource...)
	draft.SemanticKinds = cloneWithCapacity(draft.SemanticKinds, len(draft.SemanticKinds)+4)
	draft.SemanticIDs = cloneWithCapacity(draft.SemanticIDs, len(draft.SemanticIDs)+4)
	draft.MappingStarts = cloneWithCapacity(draft.MappingStarts, len(draft.MappingStarts)+4)
	draft.MappingCounts = cloneWithCapacity(draft.MappingCounts, len(draft.MappingCounts)+4)
	draft.MappingProposalItems = cloneWithCapacity(draft.MappingProposalItems, len(draft.MappingProposalItems)+4)
	second, diagnostics, err := reviewer.DraftDigest(document, proposal, draft, public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("second DraftDigest() = diagnostics %#v, error %v", diagnostics, err)
	}
	if first != second {
		t.Fatalf("draft digest changed by backing storage: %x != %x", first, second)
	}
}

func TestReviewedDraftRejectsInvalidMappings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*public.ReviewedDraft)
	}{
		{name: "empty source", mutate: func(draft *public.ReviewedDraft) { draft.PolicySource = nil }},
		{name: "column mismatch", mutate: func(draft *public.ReviewedDraft) { draft.MappingCounts = nil }},
		{name: "zero semantic ID", mutate: func(draft *public.ReviewedDraft) { draft.SemanticIDs[0] = 0 }},
		{name: "invalid semantic kind", mutate: func(draft *public.ReviewedDraft) { draft.SemanticKinds[0] = public.SemanticKindInvalid }},
		{name: "duplicate semantic row", mutate: func(draft *public.ReviewedDraft) {
			draft.SemanticKinds = append(draft.SemanticKinds, public.SemanticKindRequirement)
			draft.SemanticIDs = append(draft.SemanticIDs, 1)
			draft.MappingStarts = append(draft.MappingStarts, 2)
			draft.MappingCounts = append(draft.MappingCounts, 1)
			draft.MappingProposalItems = append(draft.MappingProposalItems, 1)
		}},
		{name: "non-owned range", mutate: func(draft *public.ReviewedDraft) { draft.MappingStarts[0] = 1 }},
		{name: "empty mapping", mutate: func(draft *public.ReviewedDraft) { draft.MappingCounts[0] = 0 }},
		{name: "invalid item", mutate: func(draft *public.ReviewedDraft) { draft.MappingProposalItems[0] = 99 }},
		{name: "no requirement row", mutate: func(draft *public.ReviewedDraft) {
			draft.MappingCounts[0] = 1
			draft.MappingProposalItems = []public.ItemID{2}
		}},
		{name: "requirement proposal mapped only to clause", mutate: func(draft *public.ReviewedDraft) {
			draft.SemanticKinds[0] = public.SemanticKindClause
		}},
		{name: "unmapped restriction", mutate: func(draft *public.ReviewedDraft) {
			draft.MappingCounts[0] = 1
			draft.MappingProposalItems = draft.MappingProposalItems[:1]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, proposal := reviewProposal(t)
			draft := validDraft()
			test.mutate(draft)
			var reviewer Reviewer
			_, diagnostics, err := reviewer.DraftDigest(document, proposal, draft, public.DefaultLimits())
			if len(diagnostics) != 0 {
				t.Fatalf("DraftDigest() diagnostics = %#v, want infrastructure error", diagnostics)
			}
			if !errors.Is(err, public.ErrInvalidDraft) {
				t.Fatalf("DraftDigest() error = %v, want ErrInvalidDraft", err)
			}
		})
	}
}

func TestApprovalTokenRoundTrip(t *testing.T) {
	document, proposal := reviewProposal(t)
	draft := validDraft()
	signer, verifier := approvalKeys(t)
	var reviewer Reviewer
	token, diagnostics, err := reviewer.IssueApproval(
		document, proposal, draft, []byte("reviewer-1"), 100, 200, signer, public.DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("IssueApproval() error = %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("IssueApproval() diagnostics = %#v", diagnostics)
	}
	if err := reviewer.VerifyApproval(document, proposal, draft, token, verifier, 150, 5, public.DefaultLimits()); err != nil {
		t.Fatalf("VerifyApproval() error = %v", err)
	}
}

func TestApprovalTokenRejectsMutationAndTimeViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*public.Document, *public.Proposal, *public.ReviewedDraft, *public.ApprovalToken)
		now    int64
		skew   int64
		want   error
	}{
		{name: "proposal", now: 150, skew: 5, want: public.ErrInvalidToken, mutate: func(_ *public.Document, proposal *public.Proposal, _ *public.ReviewedDraft, _ *public.ApprovalToken) {
			proposal.ItemBytes[0] ^= 1
		}},
		{name: "draft", now: 150, skew: 5, want: public.ErrInvalidToken, mutate: func(_ *public.Document, _ *public.Proposal, draft *public.ReviewedDraft, _ *public.ApprovalToken) {
			draft.PolicySource[1] = 'x'
		}},
		{name: "semantic mapping", now: 150, skew: 5, want: public.ErrInvalidToken, mutate: func(_ *public.Document, _ *public.Proposal, draft *public.ReviewedDraft, _ *public.ApprovalToken) {
			draft.SemanticIDs[0]++
		}},
		{name: "reviewer", now: 150, skew: 5, want: public.ErrInvalidToken, mutate: func(_ *public.Document, _ *public.Proposal, _ *public.ReviewedDraft, token *public.ApprovalToken) {
			token.Reviewer[0] ^= 1
		}},
		{name: "signature", now: 150, skew: 5, want: public.ErrInvalidToken, mutate: func(_ *public.Document, _ *public.Proposal, _ *public.ReviewedDraft, token *public.ApprovalToken) {
			token.Signature[0] ^= 1
		}},
		{name: "schema", now: 150, skew: 5, want: public.ErrInvalidToken, mutate: func(_ *public.Document, _ *public.Proposal, _ *public.ReviewedDraft, token *public.ApprovalToken) {
			token.SchemaVersion++
		}},
		{name: "expired", now: 200, skew: 5, want: public.ErrExpiredToken, mutate: func(_ *public.Document, _ *public.Proposal, _ *public.ReviewedDraft, _ *public.ApprovalToken) {}},
		{name: "future issue", now: 90, skew: 5, want: public.ErrInvalidToken, mutate: func(_ *public.Document, _ *public.Proposal, _ *public.ReviewedDraft, _ *public.ApprovalToken) {}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, proposal := reviewProposal(t)
			draft := validDraft()
			signer, verifier := approvalKeys(t)
			var reviewer Reviewer
			token, diagnostics, err := reviewer.IssueApproval(document, proposal, draft, []byte("reviewer-1"), 100, 200, signer, public.DefaultLimits())
			if err != nil || len(diagnostics) != 0 {
				t.Fatalf("IssueApproval() = diagnostics %#v, error %v", diagnostics, err)
			}
			test.mutate(document, proposal, draft, &token)
			err = reviewer.VerifyApproval(document, proposal, draft, token, verifier, test.now, test.skew, public.DefaultLimits())
			if !errors.Is(err, test.want) {
				t.Fatalf("VerifyApproval() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestApprovalTokenClassifiesTimesAtUnixBoundary(t *testing.T) {
	document, proposal := reviewProposal(t)
	draft := validDraft()
	signer, verifier := approvalKeys(t)
	tests := []struct {
		name        string
		issuedUnix  int64
		expiresUnix int64
		nowUnix     int64
		want        error
	}{
		{name: "future within skew", issuedUnix: math.MaxInt64 - 2, expiresUnix: math.MaxInt64, nowUnix: math.MaxInt64 - 3},
		{name: "expired", issuedUnix: math.MaxInt64 - 10, expiresUnix: math.MaxInt64 - 1, nowUnix: math.MaxInt64, want: public.ErrExpiredToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var reviewer Reviewer
			token, diagnostics, err := reviewer.IssueApproval(
				document, proposal, draft, []byte("reviewer-1"), test.issuedUnix, test.expiresUnix, signer, public.DefaultLimits(),
			)
			if err != nil || len(diagnostics) != 0 {
				t.Fatalf("IssueApproval() = diagnostics %#v, error %v", diagnostics, err)
			}
			err = reviewer.VerifyApproval(document, proposal, draft, token, verifier, test.nowUnix, 5, public.DefaultLimits())
			if !errors.Is(err, test.want) {
				t.Fatalf("VerifyApproval() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestApprovalPropagatesSignerAndRejectsVerifierFailure(t *testing.T) {
	document, proposal := reviewProposal(t)
	draft := validDraft()
	wantSigner := errors.New("signer unavailable")
	var reviewer Reviewer
	_, diagnostics, err := reviewer.IssueApproval(
		document, proposal, draft, []byte("reviewer-1"), 100, 200,
		failingSigner{err: wantSigner}, public.DefaultLimits(),
	)
	if len(diagnostics) != 0 {
		t.Fatalf("IssueApproval() diagnostics = %#v", diagnostics)
	}
	if !errors.Is(err, wantSigner) {
		t.Fatalf("IssueApproval() error = %v, want signer error", err)
	}

	signer, _ := approvalKeys(t)
	token, diagnostics, err := reviewer.IssueApproval(document, proposal, draft, []byte("reviewer-1"), 100, 200, signer, public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("IssueApproval() = diagnostics %#v, error %v", diagnostics, err)
	}
	if err := reviewer.VerifyApproval(document, proposal, draft, token, failingVerifier{}, 150, 5, public.DefaultLimits()); !errors.Is(err, public.ErrInvalidToken) {
		t.Fatalf("VerifyApproval() error = %v, want ErrInvalidToken", err)
	}
}

func TestApprovalTokenLimit(t *testing.T) {
	document, proposal := reviewProposal(t)
	draft := validDraft()
	signer, _ := approvalKeys(t)
	limits := public.DefaultLimits()
	limits.MaxTokenBytes = 8
	var reviewer Reviewer
	_, diagnostics, err := reviewer.IssueApproval(document, proposal, draft, []byte("reviewer-1"), 100, 200, signer, limits)
	if len(diagnostics) != 0 {
		t.Fatalf("IssueApproval() diagnostics = %#v", diagnostics)
	}
	if !errors.Is(err, public.ErrLimit) {
		t.Fatalf("IssueApproval() error = %v, want ErrLimit", err)
	}
}

func TestApprovalTokenRejectsGuaranteedOversizeBeforeSigning(t *testing.T) {
	document, proposal := reviewProposal(t)
	draft := validDraft()
	reviewerID := make([]byte, 128)
	for row := range reviewerID {
		reviewerID[row] = 'x'
	}
	limits := public.DefaultLimits()
	limits.MaxTokenBytes = 128
	var reviewer Reviewer
	_, diagnostics, err := reviewer.IssueApproval(document, proposal, draft, reviewerID, 100, 200, panicSigner{}, limits)
	if len(diagnostics) != 0 {
		t.Fatalf("IssueApproval() diagnostics = %#v", diagnostics)
	}
	if !errors.Is(err, public.ErrLimit) {
		t.Fatalf("IssueApproval() error = %v, want ErrLimit", err)
	}
}

func TestApprovalTokenLimitIncludesWireMagic(t *testing.T) {
	document, proposal := reviewProposal(t)
	draft := validDraft()
	signer, _ := approvalKeys(t)
	limits := public.DefaultLimits()
	// NRAT(4) + fixed payload(90) + reviewer-1(10) + Ed25519(64) = 168.
	limits.MaxTokenBytes = 167
	var reviewer Reviewer
	_, diagnostics, err := reviewer.IssueApproval(document, proposal, draft, []byte("reviewer-1"), 100, 200, signer, limits)
	if len(diagnostics) != 0 {
		t.Fatalf("IssueApproval() diagnostics = %#v", diagnostics)
	}
	if !errors.Is(err, public.ErrLimit) {
		t.Fatalf("IssueApproval() error = %v, want ErrLimit", err)
	}
}

func validDraft() *public.ReviewedDraft {
	return &public.ReviewedDraft{
		PolicySource:         []byte(`{"schema_version":1}`),
		SemanticKinds:        []public.SemanticKind{public.SemanticKindRequirement},
		SemanticIDs:          []uint32{1},
		MappingStarts:        []uint32{0},
		MappingCounts:        []uint16{2},
		MappingProposalItems: []public.ItemID{1, 2},
	}
}

type ed25519Signer struct{ key ed25519.PrivateKey }

func (signer ed25519Signer) Sign(message []byte) ([]byte, error) {
	return ed25519.Sign(signer.key, message), nil
}

type ed25519Verifier struct{ key ed25519.PublicKey }

func (verifier ed25519Verifier) Verify(message, signature []byte) error {
	if !ed25519.Verify(verifier.key, message, signature) {
		return errors.New("invalid signature")
	}
	return nil
}

func approvalKeys(tb testing.TB) (public.Signer, public.Verifier) {
	tb.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for row := range seed {
		seed[row] = byte(row + 1)
	}
	private := ed25519.NewKeyFromSeed(seed)
	publicKey := append(ed25519.PublicKey(nil), private.Public().(ed25519.PublicKey)...)
	return ed25519Signer{key: private}, ed25519Verifier{key: publicKey}
}

type failingSigner struct{ err error }

func (signer failingSigner) Sign([]byte) ([]byte, error) { return nil, signer.err }

type failingVerifier struct{}

func (failingVerifier) Verify([]byte, []byte) error { return errors.New("verification unavailable") }
