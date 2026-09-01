package diff

// CandidateField owns one field binding in a replayable witness.
type CandidateField struct {
	Name  string
	Value Value
}

// Evaluation owns the decision and provenance for one side of a witness.
type Evaluation struct {
	RequirementIDs       []uint32
	DriverRequirements   []uint32
	DriverClauses        []uint32
	DriverNodes          []uint32
	DriverReasons        []uint32
	DriverExplanations   []uint32
	EvidenceIDs          []uint32
	ReasonIDs            []uint32
	ReasonNodes          []uint32
	ReasonEvidenceIDs    []uint32
	ReasonEvidenceStates []uint32
	RemediationIDs       []uint32

	Index       uint64
	SourceStart uint32
	SourceEnd   uint32
	OutcomeID   uint32
	Decision    Decision
}

// Counterexample owns the smallest differing candidate and both evaluations.
type Counterexample struct {
	Fields   []CandidateField
	Evidence []Evidence
	Old      Evaluation
	New      Evaluation
	Index    uint64
}

// Result is the owned bounded comparison result.
type Result struct {
	Uncertainty       string
	Counterexample    Counterexample
	Transitions       [16]uint64
	Candidates        uint64
	Outcome           Outcome
	Complete          bool
	Forbidden         bool
	HasCounterexample bool
}
