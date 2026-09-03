package jsondiff

import (
	"encoding/hex"
	"strconv"

	"github.com/sebishogun/nornrune/internal/adapters/wire"
	policydiff "github.com/sebishogun/nornrune/policy/diff"
)

func AppendResultJSON(dst []byte, result policydiff.Result) []byte {
	dst = append(dst, `{"outcome":`...)
	dst = wire.AppendJSONStringString(dst, result.Outcome.String())
	dst = append(dst, `,"complete":`...)
	dst = strconv.AppendBool(dst, result.Complete)
	dst = append(dst, `,"forbidden":`...)
	dst = strconv.AppendBool(dst, result.Forbidden)
	dst = append(dst, `,"candidates":`...)
	dst = strconv.AppendUint(dst, result.Candidates, 10)
	dst = append(dst, `,"counterexample":`...)
	if !result.HasCounterexample {
		dst = append(dst, "null"...)
	} else {
		dst = appendCounterexampleJSON(dst, result.Counterexample)
	}
	dst = append(dst, `,"forbidden_counterexample":`...)
	if !result.HasForbiddenCounterexample {
		dst = append(dst, "null"...)
	} else {
		dst = appendCounterexampleJSON(dst, result.ForbiddenCounterexample)
	}
	dst = append(dst, `,"transitions":[`...)
	for index, count := range result.Transitions {
		if index != 0 {
			dst = append(dst, ',')
		}
		oldDecision := policydiff.Decision(index/4 + 1)
		newDecision := policydiff.Decision(index%4 + 1)
		dst = append(dst, `{"old":`...)
		dst = wire.AppendJSONStringString(dst, oldDecision.String())
		dst = append(dst, `,"new":`...)
		dst = wire.AppendJSONStringString(dst, newDecision.String())
		dst = append(dst, `,"count":`...)
		dst = strconv.AppendUint(dst, count, 10)
		dst = append(dst, `,"forbidden_count":`...)
		dst = strconv.AppendUint(dst, result.ForbiddenTransitions[index], 10)
		dst = append(dst, '}')
	}
	dst = append(dst, ']')
	dst = append(dst, `,"uncertainty":`...)
	dst = wire.AppendJSONStringString(dst, result.Uncertainty)
	return append(dst, '}', '\n')
}

func appendCounterexampleJSON(dst []byte, counterexample policydiff.Counterexample) []byte {
	dst = append(dst, `{"index":`...)
	dst = strconv.AppendUint(dst, counterexample.Index, 10)
	dst = append(dst, `,"fields":[`...)
	for row := range counterexample.Fields {
		if row != 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, `{"name":`...)
		dst = wire.AppendJSONStringString(dst, counterexample.Fields[row].Name)
		dst = append(dst, `,"value":`...)
		dst = appendValueJSON(dst, counterexample.Fields[row].Value)
		dst = append(dst, '}')
	}
	dst = append(dst, `],"evidence":[`...)
	for row := range counterexample.Evidence {
		if row != 0 {
			dst = append(dst, ',')
		}
		record := &counterexample.Evidence[row]
		dst = append(dst, `{"kind":`...)
		dst = wire.AppendJSONStringString(dst, record.Kind)
		dst = append(dst, `,"state":`...)
		dst = wire.AppendJSONStringString(dst, record.State)
		dst = append(dst, `,"subject":`...)
		dst = wire.AppendJSONStringString(dst, record.Subject)
		dst = append(dst, `,"scope":`...)
		dst = wire.AppendJSONStringString(dst, record.Scope)
		dst = append(dst, `,"timing":`...)
		dst = wire.AppendJSONStringString(dst, record.Timing)
		dst = append(dst, '}')
	}
	dst = append(dst, `],"old":`...)
	dst = appendEvaluationJSON(dst, counterexample.Old)
	dst = append(dst, `,"new":`...)
	dst = appendEvaluationJSON(dst, counterexample.New)
	return append(dst, '}')
}

func appendValueJSON(dst []byte, value policydiff.Value) []byte {
	dst = append(dst, `{"state":`...)
	state := "missing"
	if value.State == policydiff.ValuePresent {
		state = "present"
	}
	dst = wire.AppendJSONStringString(dst, state)
	dst = append(dst, `,"kind":`...)
	dst = wire.AppendJSONStringString(dst, fieldKindName(value.Kind))
	switch value.Kind {
	case policydiff.FieldKindString:
		dst = append(dst, `,"string":`...)
		dst = wire.AppendJSONStringString(dst, value.String)
	case policydiff.FieldKindInteger, policydiff.FieldKindTimestamp:
		dst = append(dst, `,"integer":`...)
		dst = strconv.AppendInt(dst, value.Integer, 10)
	case policydiff.FieldKindBoolean:
		dst = append(dst, `,"boolean":`...)
		dst = strconv.AppendBool(dst, value.Boolean)
	}
	return append(dst, '}')
}

func appendEvaluationJSON(dst []byte, evaluation policydiff.Evaluation) []byte {
	dst = append(dst, `{"decision":`...)
	dst = wire.AppendJSONStringString(dst, evaluation.Decision.String())
	dst = append(dst, `,"outcome_id":`...)
	dst = strconv.AppendUint(dst, uint64(evaluation.OutcomeID), 10)
	dst = append(dst, `,"source_start":`...)
	dst = strconv.AppendUint(dst, uint64(evaluation.SourceStart), 10)
	dst = append(dst, `,"source_end":`...)
	dst = strconv.AppendUint(dst, uint64(evaluation.SourceEnd), 10)
	dst = appendUint32JSON(dst, `,"requirements":`, evaluation.RequirementIDs)
	dst = appendUint32JSON(dst, `,"driver_requirements":`, evaluation.DriverRequirements)
	dst = appendUint32JSON(dst, `,"driver_clauses":`, evaluation.DriverClauses)
	dst = appendUint32JSON(dst, `,"driver_nodes":`, evaluation.DriverNodes)
	dst = appendUint32JSON(dst, `,"driver_reasons":`, evaluation.DriverReasons)
	dst = appendUint32JSON(dst, `,"driver_explanations":`, evaluation.DriverExplanations)
	dst = appendUint32JSON(dst, `,"evidence_ids":`, evaluation.EvidenceIDs)
	dst = appendUint32JSON(dst, `,"reason_ids":`, evaluation.ReasonIDs)
	dst = appendUint32JSON(dst, `,"reason_nodes":`, evaluation.ReasonNodes)
	dst = appendUint32JSON(dst, `,"reason_evidence_ids":`, evaluation.ReasonEvidenceIDs)
	dst = appendUint32JSON(dst, `,"reason_evidence_states":`, evaluation.ReasonEvidenceStates)
	dst = appendUint32JSON(dst, `,"remediation_ids":`, evaluation.RemediationIDs)
	dst = appendDigestJSON(dst, `,"assumptions_digest":`, evaluation.AssumptionsDigest)
	dst = appendDigestJSON(dst, `,"driver_templates_digest":`, evaluation.DriverTemplatesDigest)
	dst = appendDigestJSON(dst, `,"evidence_issues_digest":`, evaluation.EvidenceIssuesDigest)
	return append(dst, '}')
}

func appendDigestJSON(dst []byte, key string, digest [32]byte) []byte {
	dst = append(dst, key...)
	dst = append(dst, '"')
	dst = hex.AppendEncode(dst, digest[:])
	return append(dst, '"')
}

func appendUint32JSON(dst []byte, key string, values []uint32) []byte {
	dst = append(dst, key...)
	dst = append(dst, '[')
	for row, value := range values {
		if row != 0 {
			dst = append(dst, ',')
		}
		dst = strconv.AppendUint(dst, uint64(value), 10)
	}
	return append(dst, ']')
}

func AppendResultText(dst []byte, result policydiff.Result) []byte {
	dst = append(dst, "Outcome: "...)
	dst = append(dst, result.Outcome.String()...)
	dst = append(dst, "\nComplete: "...)
	dst = strconv.AppendBool(dst, result.Complete)
	dst = append(dst, "\nForbidden: "...)
	dst = strconv.AppendBool(dst, result.Forbidden)
	dst = append(dst, "\nCandidates: "...)
	dst = strconv.AppendUint(dst, result.Candidates, 10)
	if result.HasCounterexample {
		dst = append(dst, "\nCounterexample: "...)
		dst = strconv.AppendUint(dst, result.Counterexample.Index, 10)
		dst = append(dst, "\nTransition: "...)
		dst = append(dst, result.Counterexample.Old.Decision.String()...)
		dst = append(dst, " -> "...)
		dst = append(dst, result.Counterexample.New.Decision.String()...)
	}
	if result.HasForbiddenCounterexample {
		dst = append(dst, "\nFirst forbidden transition: "...)
		dst = append(dst, result.ForbiddenCounterexample.Old.Decision.String()...)
		dst = append(dst, " -> "...)
		dst = append(dst, result.ForbiddenCounterexample.New.Decision.String()...)
	}
	for index, count := range result.Transitions {
		if count == 0 {
			continue
		}
		dst = append(dst, "\nTransition "...)
		dst = append(dst, policydiff.Decision(index/4+1).String()...)
		dst = append(dst, " -> "...)
		dst = append(dst, policydiff.Decision(index%4+1).String()...)
		dst = append(dst, ": "...)
		dst = strconv.AppendUint(dst, count, 10)
		dst = append(dst, " (forbidden "...)
		dst = strconv.AppendUint(dst, result.ForbiddenTransitions[index], 10)
		dst = append(dst, ')')
	}
	if result.Uncertainty != "" {
		dst = append(dst, "\nUncertainty: "...)
		dst = append(dst, result.Uncertainty...)
	}
	return append(dst, '\n')
}

func fieldKindName(kind policydiff.FieldKind) string {
	switch kind {
	case policydiff.FieldKindString:
		return "string"
	case policydiff.FieldKindInteger:
		return "integer"
	case policydiff.FieldKindBoolean:
		return "boolean"
	case policydiff.FieldKindTimestamp:
		return "timestamp"
	case policydiff.FieldKindPresence:
		return "presence"
	default:
		return "invalid"
	}
}
