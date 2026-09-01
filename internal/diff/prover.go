package diff

import (
	"context"
	"errors"
	"strings"
)

var ErrInvalidProof = errors.New("policy diff: invalid proof")

// ProofClaim is one bounded provider assertion.
type ProofClaim uint8

const (
	ProofClaimInvalid ProofClaim = iota
	ProofClaimEquivalent
	ProofClaimChanged
	ProofClaimInconclusive
)

func (claim ProofClaim) Valid() bool {
	return claim >= ProofClaimEquivalent && claim <= ProofClaimInconclusive
}

// Candidate is an owned provider witness with expected concrete decisions.
type Candidate struct {
	Fields   []CandidateField
	Evidence []Evidence

	OldDecision Decision
	NewDecision Decision
}

// Proof is one advisory provider response.
type Proof struct {
	Witness Candidate
	Claim   ProofClaim
}

// ProofRequest owns every slice and string visible to a provider.
type ProofRequest struct {
	OldSource []byte
	NewSource []byte
	Fields    FieldSchema
	Domain    Domain
	Matrix    RiskMatrix
}

// Prover may propose an equivalence claim or a candidate witness.
type Prover interface {
	Prove(context.Context, ProofRequest) (Proof, error)
}

func invokeProver(ctx context.Context, prover Prover, request ProofRequest) (proof Proof, err error) {
	defer func() {
		if recover() != nil {
			proof = Proof{}
			err = ErrInvalidProof
		}
	}()
	return prover.Prove(ctx, request)
}

func ownProofRequest(oldSource, newSource []byte, fields FieldSchema, domain Domain, matrix RiskMatrix) ProofRequest {
	request := ProofRequest{
		OldSource: append([]byte(nil), oldSource...),
		NewSource: append([]byte(nil), newSource...),
		Domain:    CloneDomain(domain),
		Matrix:    matrix,
	}
	request.Fields.Fields = make([]FieldSpec, len(fields.Fields))
	for row := range fields.Fields {
		request.Fields.Fields[row] = fields.Fields[row]
		request.Fields.Fields[row].Name = strings.Clone(fields.Fields[row].Name)
	}
	return request
}
