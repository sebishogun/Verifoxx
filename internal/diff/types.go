// Package diff compares compiled policy behavior over explicit finite domains.
package diff

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidEnum         = errors.New("policy diff: invalid enum")
	ErrInvalidRiskMatrix   = errors.New("policy diff: invalid risk matrix")
	ErrInvalidDomain       = errors.New("policy diff: invalid domain")
	ErrCandidateBudget     = errors.New("policy diff: candidate budget exhausted")
	ErrInvalidFieldSchema  = errors.New("policy diff: invalid field schema")
	ErrInvalidPolicy       = errors.New("policy diff: invalid policy")
	ErrUnsupportedOutcomes = errors.New("policy diff: unsupported outcome catalog")
)

// Outcome classifies the complete observed policy-pair behavior.
type Outcome uint8

const (
	OutcomeInvalid Outcome = iota
	Equivalent
	Widened
	Narrowed
	Changed
	Inconclusive
)

var outcomeNames = [...]string{"equivalent", "widened", "narrowed", "changed", "inconclusive"}

func (outcome Outcome) Valid() bool { return outcome >= Equivalent && outcome <= Inconclusive }

func (outcome Outcome) String() string {
	row := int(outcome) - 1
	if row < 0 || row >= len(outcomeNames) {
		return "invalid"
	}
	return outcomeNames[row]
}

func (outcome Outcome) MarshalText() ([]byte, error) {
	if !outcome.Valid() {
		return nil, ErrInvalidEnum
	}
	return []byte(outcome.String()), nil
}

func (outcome *Outcome) UnmarshalText(text []byte) error {
	if outcome == nil {
		return ErrInvalidEnum
	}
	for row, name := range outcomeNames {
		if string(text) == name {
			*outcome = Outcome(row + 1)
			return nil
		}
	}
	return fmt.Errorf("%w: outcome %q", ErrInvalidEnum, text)
}

// Decision is one of NornRune's four stable policy decisions.
type Decision uint8

const (
	DecisionInvalid Decision = iota
	Approve
	Reject
	Revise
	Escalate
)

var decisionNames = [...]string{"Approve", "Reject", "Revise", "Escalate"}

func (decision Decision) Valid() bool { return decision >= Approve && decision <= Escalate }

func (decision Decision) String() string {
	row := int(decision) - 1
	if row < 0 || row >= len(decisionNames) {
		return "invalid"
	}
	return decisionNames[row]
}

// Transition classifies and authorizes one old/new decision pair.
type Transition struct {
	Class   Outcome
	Allowed bool
}

// RiskMatrix stores old-major transitions for four old and four new decisions.
type RiskMatrix struct {
	Transitions [16]Transition
}

func transitionIndex(old, next Decision) (int, bool) {
	if !old.Valid() || !next.Valid() {
		return 0, false
	}
	return int(old-1)*4 + int(next-1), true
}

// Set assigns one decision transition.
func (matrix *RiskMatrix) Set(old, next Decision, transition Transition) error {
	index, ok := transitionIndex(old, next)
	if matrix == nil || !ok || !validTransition(old, next, transition) {
		return ErrInvalidRiskMatrix
	}
	matrix.Transitions[index] = transition
	return nil
}

// Lookup returns one valid transition.
func (matrix RiskMatrix) Lookup(old, next Decision) (Transition, bool) {
	index, ok := transitionIndex(old, next)
	if !ok {
		return Transition{}, false
	}
	transition := matrix.Transitions[index]
	return transition, validTransition(old, next, transition)
}

// Validate checks every matrix row.
func (matrix RiskMatrix) Validate() error {
	for old := Approve; old <= Escalate; old++ {
		for next := Approve; next <= Escalate; next++ {
			if _, ok := matrix.Lookup(old, next); !ok {
				return ErrInvalidRiskMatrix
			}
		}
	}
	return nil
}

func validTransition(old, next Decision, transition Transition) bool {
	if !transition.Class.Valid() || transition.Class == Inconclusive {
		return false
	}
	if old == next {
		return transition.Class == Equivalent
	}
	return transition.Class == Widened || transition.Class == Narrowed || transition.Class == Changed
}
