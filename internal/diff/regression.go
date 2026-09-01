package diff

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/sebishogun/nornrune/internal/jsonstrict"
)

var ErrInvalidExceptions = errors.New("policy diff: invalid exceptions")

// Exception authorizes one exact forbidden transition until an explicit expiry.
type Exception struct {
	Expires time.Time
	ID      string
	Reason  string
	Owner   string

	OldDigest     [32]byte
	NewDigest     [32]byte
	WitnessDigest [32]byte
	OldDecision   Decision
	NewDecision   Decision
}

// RegressionDecision reports whether CI may accept a comparison result.
type RegressionDecision struct {
	ExceptionID string
	Reason      string
	Allowed     bool
}

// SourceDigest returns the exact source SHA-256 used by exception matching.
func SourceDigest(source []byte) [32]byte { return sha256.Sum256(source) }

// CounterexampleDigest returns a deterministic digest of an owned witness.
func CounterexampleDigest(counterexample Counterexample) [32]byte {
	encoded := appendCounterexample(nil, counterexample)
	return sha256.Sum256(encoded)
}

// CheckRegression applies current exact-match exceptions without reading a clock.
func CheckRegression(result Result, oldSource, newSource []byte, exceptions []Exception, now time.Time) RegressionDecision {
	if result.Outcome == Inconclusive {
		return RegressionDecision{Reason: "comparison is inconclusive"}
	}
	if !result.Forbidden {
		return RegressionDecision{Allowed: true, Reason: "no forbidden transition"}
	}
	if !result.HasCounterexample {
		return RegressionDecision{Reason: "forbidden result has no counterexample"}
	}
	oldDigest := SourceDigest(oldSource)
	newDigest := SourceDigest(newSource)
	witnessDigest := CounterexampleDigest(result.Counterexample)
	now = now.UTC()
	for row := range exceptions {
		exception := &exceptions[row]
		if !validException(*exception) || !now.Before(exception.Expires.UTC()) ||
			exception.OldDigest != oldDigest || exception.NewDigest != newDigest || exception.WitnessDigest != witnessDigest ||
			exception.OldDecision != result.Counterexample.Old.Decision || exception.NewDecision != result.Counterexample.New.Decision {
			continue
		}
		return RegressionDecision{Allowed: true, ExceptionID: exception.ID, Reason: exception.Reason}
	}
	return RegressionDecision{Reason: "forbidden transition has no current exact-match exception"}
}

type exceptionJSON struct {
	ID            string `json:"id"`
	Reason        string `json:"reason"`
	Owner         string `json:"owner"`
	OldDigest     string `json:"old_digest"`
	NewDigest     string `json:"new_digest"`
	WitnessDigest string `json:"witness_digest"`
	OldDecision   string `json:"old_decision"`
	NewDecision   string `json:"new_decision"`
	Expires       string `json:"expires"`
}

// DecodeExceptions strictly decodes a bounded JSON exception array.
func DecodeExceptions(source []byte, maximum int) ([]Exception, error) {
	if maximum <= 0 {
		return nil, ErrInvalidExceptions
	}
	if jsonstrict.Validate(source, 32) != nil {
		return nil, ErrInvalidExceptions
	}
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	var rows []exceptionJSON
	if err := decoder.Decode(&rows); err != nil || rows == nil || len(rows) > maximum {
		return nil, ErrInvalidExceptions
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ErrInvalidExceptions
	}
	exceptions := make([]Exception, len(rows))
	for row := range rows {
		exception, err := decodeException(rows[row])
		if err != nil {
			return nil, err
		}
		for previous := 0; previous < row; previous++ {
			if exceptions[previous].ID == exception.ID {
				return nil, ErrInvalidExceptions
			}
		}
		exceptions[row] = exception
	}
	return exceptions, nil
}

func decodeException(source exceptionJSON) (Exception, error) {
	exception := Exception{ID: source.ID, Reason: source.Reason, Owner: source.Owner}
	var err error
	if exception.OldDigest, err = decodeDigest(source.OldDigest); err != nil {
		return Exception{}, err
	}
	if exception.NewDigest, err = decodeDigest(source.NewDigest); err != nil {
		return Exception{}, err
	}
	if exception.WitnessDigest, err = decodeDigest(source.WitnessDigest); err != nil {
		return Exception{}, err
	}
	if exception.OldDecision, err = parseDecision(source.OldDecision); err != nil {
		return Exception{}, err
	}
	if exception.NewDecision, err = parseDecision(source.NewDecision); err != nil {
		return Exception{}, err
	}
	exception.Expires, err = time.Parse(time.RFC3339, source.Expires)
	if err != nil {
		return Exception{}, ErrInvalidExceptions
	}
	_, offset := exception.Expires.Zone()
	if offset != 0 || !validException(exception) {
		return Exception{}, ErrInvalidExceptions
	}
	exception.Expires = exception.Expires.UTC()
	return exception, nil
}

func decodeDigest(source string) ([32]byte, error) {
	var digest [32]byte
	decoded, err := hex.DecodeString(source)
	if err != nil || len(decoded) != len(digest) {
		return digest, ErrInvalidExceptions
	}
	copy(digest[:], decoded)
	return digest, nil
}

func parseDecision(source string) (Decision, error) {
	for decision := Approve; decision <= Escalate; decision++ {
		if decision.String() == source {
			return decision, nil
		}
	}
	return DecisionInvalid, ErrInvalidExceptions
}

func validException(exception Exception) bool {
	return exception.ID != "" && exception.Reason != "" && exception.Owner != "" &&
		!exception.Expires.IsZero() && exception.OldDecision.Valid() && exception.NewDecision.Valid()
}

func appendCounterexample(dst []byte, counterexample Counterexample) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, counterexample.Index)
	dst = appendFields(dst, counterexample.Fields)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(counterexample.Evidence)))
	for row := range counterexample.Evidence {
		dst = appendText(dst, counterexample.Evidence[row].Kind)
		dst = appendText(dst, counterexample.Evidence[row].State)
		dst = appendText(dst, counterexample.Evidence[row].Subject)
		dst = appendText(dst, counterexample.Evidence[row].Scope)
		dst = appendText(dst, counterexample.Evidence[row].Timing)
	}
	dst = appendEvaluation(dst, counterexample.Old)
	return appendEvaluation(dst, counterexample.New)
}

func appendFields(dst []byte, fields []CandidateField) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(fields)))
	for row := range fields {
		dst = appendText(dst, fields[row].Name)
		dst = appendText(dst, fields[row].Value.String)
		dst = binary.LittleEndian.AppendUint64(dst, uint64(fields[row].Value.Integer))
		dst = append(dst, byte(fields[row].Value.State), byte(fields[row].Value.Kind))
		if fields[row].Value.Boolean {
			dst = append(dst, 1)
		} else {
			dst = append(dst, 0)
		}
	}
	return dst
}

func appendEvaluation(dst []byte, evaluation Evaluation) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, evaluation.Index)
	for _, value := range []uint32{evaluation.SourceStart, evaluation.SourceEnd, evaluation.OutcomeID, uint32(evaluation.Decision)} {
		dst = binary.LittleEndian.AppendUint32(dst, value)
	}
	for _, values := range [][]uint32{
		evaluation.RequirementIDs, evaluation.DriverRequirements, evaluation.DriverClauses, evaluation.DriverNodes,
		evaluation.DriverReasons, evaluation.DriverExplanations, evaluation.EvidenceIDs, evaluation.ReasonIDs,
		evaluation.ReasonNodes, evaluation.ReasonEvidenceIDs, evaluation.ReasonEvidenceStates, evaluation.RemediationIDs,
	} {
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(values)))
		for _, value := range values {
			dst = binary.LittleEndian.AppendUint32(dst, value)
		}
	}
	return dst
}

func appendText(dst []byte, value string) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}
