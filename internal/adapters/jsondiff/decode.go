// Package jsondiff decodes bounded semantic-diff configuration and encodes results.
package jsondiff

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/sebishogun/nornrune/internal/jsonstrict"
	policydiff "github.com/sebishogun/nornrune/policy/diff"
)

var ErrInvalidConfig = errors.New("jsondiff: invalid configuration")

type Limits struct {
	MaxBytes           int
	MaxFields          int
	MaxValues          int
	MaxEvidenceSets    int
	MaxEvidenceRecords int
}

type Config struct {
	Fields policydiff.FieldSchema
	Domain policydiff.Domain
	Matrix policydiff.RiskMatrix
}

type configJSON struct {
	Fields        []fieldJSON       `json:"fields"`
	EvidenceSets  []evidenceSetJSON `json:"evidence_sets"`
	Transitions   []transitionJSON  `json:"transitions"`
	MaxCandidates uint64            `json:"max_candidates"`
	BatchRows     uint32            `json:"batch_rows"`
}

type fieldJSON struct {
	Name   string      `json:"name"`
	Kind   string      `json:"kind"`
	Group  string      `json:"group"`
	Values []valueJSON `json:"values"`
	Closed bool        `json:"closed"`
}

type valueJSON struct {
	State   string `json:"state"`
	String  string `json:"string"`
	Integer int64  `json:"integer"`
	Boolean bool   `json:"boolean"`
}

type evidenceSetJSON struct {
	Records []evidenceJSON `json:"records"`
}

type evidenceJSON struct {
	Kind    string `json:"kind"`
	State   string `json:"state"`
	Subject string `json:"subject"`
	Scope   string `json:"scope"`
	Timing  string `json:"timing"`
}

type transitionJSON struct {
	Old     string `json:"old"`
	New     string `json:"new"`
	Class   string `json:"class"`
	Allowed bool   `json:"allowed"`
}

func DecodeConfig(source []byte, limits Limits) (Config, error) {
	if limits.MaxBytes <= 0 || limits.MaxFields <= 0 || limits.MaxValues <= 0 || limits.MaxEvidenceSets <= 0 ||
		limits.MaxEvidenceRecords <= 0 || len(source) == 0 || len(source) > limits.MaxBytes {
		return Config{}, ErrInvalidConfig
	}
	if jsonstrict.Validate(source, 64) != nil {
		return Config{}, ErrInvalidConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	var decoded configJSON
	if err := decoder.Decode(&decoded); err != nil {
		return Config{}, ErrInvalidConfig
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Config{}, ErrInvalidConfig
	}
	if len(decoded.Fields) == 0 || len(decoded.Fields) > limits.MaxFields || len(decoded.EvidenceSets) > limits.MaxEvidenceSets || len(decoded.Transitions) != 16 {
		return Config{}, ErrInvalidConfig
	}
	config := Config{}
	config.Fields.Fields = make([]policydiff.FieldSpec, len(decoded.Fields))
	config.Domain.Fields = make([]policydiff.FieldDomain, len(decoded.Fields))
	config.Domain.MaxCandidates = decoded.MaxCandidates
	config.Domain.BatchRows = decoded.BatchRows
	valueCount := 0
	for row := range decoded.Fields {
		kind, ok := decodeKind(decoded.Fields[row].Kind)
		if !ok {
			return Config{}, ErrInvalidConfig
		}
		group, ok := decodeGroup(decoded.Fields[row].Group)
		if !ok || len(decoded.Fields[row].Values) == 0 {
			return Config{}, ErrInvalidConfig
		}
		valueCount += len(decoded.Fields[row].Values)
		if valueCount > limits.MaxValues {
			return Config{}, ErrInvalidConfig
		}
		config.Fields.Fields[row] = policydiff.FieldSpec{Name: decoded.Fields[row].Name, Kind: kind, Group: group}
		field := policydiff.FieldDomain{
			Name: decoded.Fields[row].Name, Kind: kind, Closed: decoded.Fields[row].Closed,
			Values: make([]policydiff.Value, len(decoded.Fields[row].Values)),
		}
		for valueRow := range decoded.Fields[row].Values {
			state, ok := decodeState(decoded.Fields[row].Values[valueRow].State)
			if !ok {
				return Config{}, ErrInvalidConfig
			}
			field.Values[valueRow] = policydiff.Value{
				String: decoded.Fields[row].Values[valueRow].String, Integer: decoded.Fields[row].Values[valueRow].Integer,
				State: state, Kind: kind, Boolean: decoded.Fields[row].Values[valueRow].Boolean,
			}
		}
		config.Domain.Fields[row] = field
	}
	config.Domain.EvidenceSets = make([]policydiff.EvidenceSet, len(decoded.EvidenceSets))
	evidenceCount := 0
	for setRow := range decoded.EvidenceSets {
		evidenceCount += len(decoded.EvidenceSets[setRow].Records)
		if evidenceCount > limits.MaxEvidenceRecords {
			return Config{}, ErrInvalidConfig
		}
		config.Domain.EvidenceSets[setRow].Records = make([]policydiff.Evidence, len(decoded.EvidenceSets[setRow].Records))
		for recordRow := range decoded.EvidenceSets[setRow].Records {
			record := decoded.EvidenceSets[setRow].Records[recordRow]
			config.Domain.EvidenceSets[setRow].Records[recordRow] = policydiff.Evidence{
				Kind: record.Kind, State: record.State, Subject: record.Subject, Scope: record.Scope, Timing: record.Timing,
			}
		}
	}
	var seen [16]bool
	for row := range decoded.Transitions {
		oldDecision, ok := decodeDecision(decoded.Transitions[row].Old)
		if !ok {
			return Config{}, ErrInvalidConfig
		}
		newDecision, ok := decodeDecision(decoded.Transitions[row].New)
		if !ok {
			return Config{}, ErrInvalidConfig
		}
		class, ok := decodeOutcome(decoded.Transitions[row].Class)
		if !ok {
			return Config{}, ErrInvalidConfig
		}
		index := int(oldDecision-1)*4 + int(newDecision-1)
		if seen[index] {
			return Config{}, ErrInvalidConfig
		}
		seen[index] = true
		if err := config.Matrix.Set(oldDecision, newDecision, policydiff.Transition{Class: class, Allowed: decoded.Transitions[row].Allowed}); err != nil {
			return Config{}, ErrInvalidConfig
		}
	}
	if _, _, err := policydiff.ValidateDomain(config.Domain); err != nil && !errors.Is(err, policydiff.ErrCandidateBudget) {
		return Config{}, ErrInvalidConfig
	}
	if config.Matrix.Validate() != nil {
		return Config{}, ErrInvalidConfig
	}
	return config, nil
}

func decodeKind(value string) (policydiff.FieldKind, bool) {
	switch value {
	case "string":
		return policydiff.FieldKindString, true
	case "integer":
		return policydiff.FieldKindInteger, true
	case "boolean":
		return policydiff.FieldKindBoolean, true
	case "timestamp":
		return policydiff.FieldKindTimestamp, true
	case "presence":
		return policydiff.FieldKindPresence, true
	default:
		return policydiff.FieldKindInvalid, false
	}
}

func decodeGroup(value string) (policydiff.FieldGroup, bool) {
	switch value {
	case "subject":
		return policydiff.FieldGroupSubject, true
	case "action":
		return policydiff.FieldGroupAction, true
	case "resource":
		return policydiff.FieldGroupResource, true
	case "output":
		return policydiff.FieldGroupOutput, true
	case "context":
		return policydiff.FieldGroupContext, true
	default:
		return policydiff.FieldGroupInvalid, false
	}
}

func decodeState(value string) (policydiff.ValueState, bool) {
	switch value {
	case "missing":
		return policydiff.ValueMissing, true
	case "present":
		return policydiff.ValuePresent, true
	default:
		return policydiff.ValueStateInvalid, false
	}
}

func decodeDecision(value string) (policydiff.Decision, bool) {
	for decision := policydiff.Approve; decision <= policydiff.Escalate; decision++ {
		if decision.String() == value {
			return decision, true
		}
	}
	return policydiff.DecisionInvalid, false
}

func decodeOutcome(value string) (policydiff.Outcome, bool) {
	for outcome := policydiff.Equivalent; outcome <= policydiff.Changed; outcome++ {
		if outcome.String() == value {
			return outcome, true
		}
	}
	return policydiff.OutcomeInvalid, false
}
