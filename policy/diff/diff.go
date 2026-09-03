// Package diff exposes bounded semantic policy comparison contracts.
package diff

import (
	"time"

	internaldiff "github.com/sebishogun/nornrune/internal/diff"
)

type Outcome = internaldiff.Outcome
type Decision = internaldiff.Decision
type Transition = internaldiff.Transition
type RiskMatrix = internaldiff.RiskMatrix
type FieldKind = internaldiff.FieldKind
type FieldGroup = internaldiff.FieldGroup
type FieldSpec = internaldiff.FieldSpec
type FieldSchema = internaldiff.FieldSchema
type ValueState = internaldiff.ValueState
type Value = internaldiff.Value
type FieldDomain = internaldiff.FieldDomain
type Evidence = internaldiff.Evidence
type EvidenceSet = internaldiff.EvidenceSet
type Domain = internaldiff.Domain
type Analyzer = internaldiff.Analyzer
type CandidateField = internaldiff.CandidateField
type Evaluation = internaldiff.Evaluation
type Counterexample = internaldiff.Counterexample
type Result = internaldiff.Result
type ProofClaim = internaldiff.ProofClaim
type Candidate = internaldiff.Candidate
type Proof = internaldiff.Proof
type ProofRequest = internaldiff.ProofRequest

// Prover is called synchronously and must return promptly when its context is done.
type Prover = internaldiff.Prover
type Exception = internaldiff.Exception
type RegressionDecision = internaldiff.RegressionDecision

const (
	OutcomeInvalid = internaldiff.OutcomeInvalid
	Equivalent     = internaldiff.Equivalent
	Widened        = internaldiff.Widened
	Narrowed       = internaldiff.Narrowed
	Changed        = internaldiff.Changed
	Inconclusive   = internaldiff.Inconclusive

	DecisionInvalid = internaldiff.DecisionInvalid
	Approve         = internaldiff.Approve
	Reject          = internaldiff.Reject
	Revise          = internaldiff.Revise
	Escalate        = internaldiff.Escalate

	FieldKindInvalid   = internaldiff.FieldKindInvalid
	FieldKindString    = internaldiff.FieldKindString
	FieldKindInteger   = internaldiff.FieldKindInteger
	FieldKindBoolean   = internaldiff.FieldKindBoolean
	FieldKindTimestamp = internaldiff.FieldKindTimestamp
	FieldKindPresence  = internaldiff.FieldKindPresence

	FieldGroupInvalid  = internaldiff.FieldGroupInvalid
	FieldGroupSubject  = internaldiff.FieldGroupSubject
	FieldGroupAction   = internaldiff.FieldGroupAction
	FieldGroupResource = internaldiff.FieldGroupResource
	FieldGroupOutput   = internaldiff.FieldGroupOutput
	FieldGroupContext  = internaldiff.FieldGroupContext

	ValueStateInvalid = internaldiff.ValueStateInvalid
	ValueMissing      = internaldiff.ValueMissing
	ValuePresent      = internaldiff.ValuePresent

	ProofClaimInvalid      = internaldiff.ProofClaimInvalid
	ProofClaimEquivalent   = internaldiff.ProofClaimEquivalent
	ProofClaimChanged      = internaldiff.ProofClaimChanged
	ProofClaimInconclusive = internaldiff.ProofClaimInconclusive

	MaxBatchRows = internaldiff.MaxBatchRows
)

var (
	ErrInvalidEnum         = internaldiff.ErrInvalidEnum
	ErrInvalidRiskMatrix   = internaldiff.ErrInvalidRiskMatrix
	ErrInvalidDomain       = internaldiff.ErrInvalidDomain
	ErrCandidateBudget     = internaldiff.ErrCandidateBudget
	ErrInvalidFieldSchema  = internaldiff.ErrInvalidFieldSchema
	ErrInvalidPolicy       = internaldiff.ErrInvalidPolicy
	ErrUnsupportedOutcomes = internaldiff.ErrUnsupportedOutcomes
	ErrInvalidProof        = internaldiff.ErrInvalidProof
	ErrInvalidExceptions   = internaldiff.ErrInvalidExceptions
)

func ValidateDomain(domain Domain) (uint64, bool, error) { return domain.Validate() }

func CloneDomain(domain Domain) Domain { return internaldiff.CloneDomain(domain) }

func SourceDigest(source []byte) [32]byte { return internaldiff.SourceDigest(source) }

func CounterexampleDigest(counterexample Counterexample) [32]byte {
	return internaldiff.CounterexampleDigest(counterexample)
}

func CheckRegression(result Result, oldSource, newSource []byte, exceptions []Exception, now time.Time) RegressionDecision {
	return internaldiff.CheckRegression(result, oldSource, newSource, exceptions, now)
}

func DecodeExceptions(source []byte, maximum int) ([]Exception, error) {
	return internaldiff.DecodeExceptions(source, maximum)
}
