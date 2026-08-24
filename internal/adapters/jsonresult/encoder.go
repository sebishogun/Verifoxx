// Package jsonresult appends deterministic machine-readable policy results.
package jsonresult

import (
	"errors"
	"strconv"

	"github.com/sebishogun/verifoxx/internal/adapters/wire"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

var (
	ErrInvalidProgram = errors.New("jsonresult: invalid program")
	ErrInvalidResult  = errors.New("jsonresult: invalid result")
)

const clauseExplanationBranchCount = 7

// Encoder retains borrowed immutable Program metadata and reusable explanation
// storage. One Encoder belongs to one sequential worker and is not concurrent.
type Encoder struct {
	program      *program.Program
	materialized result.Materialized
	explainer    result.Explainer
}

// Bind validates p into temporary state and replaces the active Program only
// after every encoder dependency succeeds.
func (e *Encoder) Bind(p *program.Program) error {
	if e == nil || p == nil {
		return ErrInvalidProgram
	}
	if e.program == p {
		return nil
	}
	var explainer result.Explainer
	if err := explainer.Bind(p.ExplanationCatalog()); err != nil || !validProgram(p) {
		return ErrInvalidProgram
	}
	e.explainer = explainer
	e.program = p
	return nil
}

func validProgram(p *program.Program) bool {
	clauses := uint64(len(p.ClauseAssertionRoots))
	if clauses == 0 || len(p.RequirementIDs) == 0 || clauses > ^uint64(0)/truth.ReasonCount ||
		clauses*clauseExplanationBranchCount != uint64(len(p.ClauseExplanationIDs)) ||
		len(p.ClauseOnSatisfied) != int(clauses) || len(p.ClauseOnFalse) != int(clauses) ||
		clauses*truth.ReasonCount != uint64(len(p.Resolutions.OutcomeIDs)) ||
		clauses*truth.ReasonCount != uint64(len(p.Resolutions.ExplanationIDs)) ||
		len(p.RequirementClauseStarts) != len(p.RequirementIDs) || len(p.RequirementClauseCounts) != len(p.RequirementIDs) {
		return false
	}
	for _, id := range p.ClauseExplanationIDs {
		if _, ok := p.Explanations.Lookup(id); !ok {
			return false
		}
	}
	for row := range p.ClauseAssertionRoots {
		if _, ok := p.Outcomes.Lookup(p.ClauseOnSatisfied[row]); !ok {
			return false
		}
		if _, ok := p.Outcomes.Lookup(p.ClauseOnFalse[row]); !ok {
			return false
		}
	}
	for row, id := range p.Resolutions.ExplanationIDs {
		if _, ok := p.Explanations.Lookup(id); !ok {
			return false
		}
		if _, ok := p.Outcomes.Lookup(p.Resolutions.OutcomeIDs[row]); !ok {
			return false
		}
	}
	for row := range p.RequirementIDs {
		start := uint64(p.RequirementClauseStarts[row])
		end := start + uint64(p.RequirementClauseCounts[row])
		if start == end || end > uint64(len(p.RequirementClauseIDs)) {
			return false
		}
		for _, id := range p.RequirementClauseIDs[int(start):int(end)] {
			if id == 0 || uint64(id) > clauses {
				return false
			}
		}
	}
	return true
}

// Append appends one complete JSON document to dst. On error it returns dst at
// its original logical length; bytes beyond that length are unspecified.
func (e *Encoder) Append(dst []byte, requestIDs []schema.RequestID, batch *result.Batch, engineVersion []byte) ([]byte, error) {
	if e == nil || e.program == nil {
		return dst, ErrInvalidProgram
	}
	if batch == nil || len(engineVersion) == 0 || uint64(len(requestIDs)) != uint64(batch.Rows) {
		return dst, ErrInvalidResult
	}
	original := dst
	p := e.program
	policyName, ok := p.Symbol(p.PolicyName)
	if !ok {
		return original, ErrInvalidProgram
	}
	policyVersion, ok := p.Symbol(p.PolicyVersion)
	if !ok {
		return original, ErrInvalidProgram
	}
	if batch.Rows == 0 && !validEmptyResult(batch) {
		return original, ErrInvalidResult
	}

	dst = append(dst, "{\n  \"schema_version\": 1,\n  \"policy\": {\n    \"name\": "...)
	dst = appendJSONString(dst, policyName)
	dst = append(dst, ",\n    \"version\": "...)
	dst = appendJSONString(dst, policyVersion)
	dst = append(dst, ",\n    \"sha256\": \""...)
	dst = appendHash(dst, p.ContentHash)
	dst = append(dst, "\"\n  },\n  \"engine_version\": "...)
	dst = appendJSONString(dst, engineVersion)
	dst = append(dst, ",\n  \"results\": ["...)
	if batch.Rows == 0 {
		return append(dst, "]\n}\n"...), nil
	}
	dst = append(dst, '\n')
	for row := uint32(0); row < batch.Rows; row++ {
		requestID := requestIDs[row]
		if requestID == 0 || e.explainer.Materialize(&e.materialized, batch, row, requestID) != nil {
			return original, ErrInvalidResult
		}
		reason, ok := rowDriver(p, batch, row, e.materialized.DriverRequirementRow)
		if !ok {
			return original, ErrInvalidResult
		}
		if row != 0 {
			dst = append(dst, ",\n"...)
		}
		dst, ok = appendRow(dst, requestID, e.materialized.Outcome, reason, batch, row, &e.materialized)
		if !ok {
			return original, ErrInvalidResult
		}
	}
	return append(dst, "\n  ]\n}\n"...), nil
}

func validEmptyResult(batch *result.Batch) bool {
	if batch == nil || batch.Rows != 0 || len(batch.OutcomeIDs) != 0 || len(batch.RequirementIDs) != 0 ||
		len(batch.DriverRequirements) != 0 || len(batch.DriverClauses) != 0 || len(batch.DriverNodes) != 0 ||
		len(batch.DriverReasons) != 0 || len(batch.DriverExplanations) != 0 || len(batch.EvidenceIDs) != 0 ||
		len(batch.ReasonIDs) != 0 || len(batch.ReasonNodes) != 0 || len(batch.ReasonEvidenceIDs) != 0 ||
		len(batch.ReasonEvidenceStates) != 0 || len(batch.RemediationIDs) != 0 {
		return false
	}
	for _, offsets := range [][]uint32{
		batch.RequirementOffsets,
		batch.DriverOffsets,
		batch.EvidenceOffsets,
		batch.ReasonOffsets,
		batch.RemediationOffsets,
	} {
		if len(offsets) != 1 || offsets[0] != 0 {
			return false
		}
	}
	return true
}

func rowDriver(p *program.Program, batch *result.Batch, row, requirementRow uint32) (string, bool) {
	driverStart := batch.DriverOffsets[row]
	driverEnd := batch.DriverOffsets[row+1]
	if driverEnd-driverStart != 1 {
		return "", false
	}
	driver := int(driverStart)
	requirementID := batch.DriverRequirements[driver]
	clauseID := batch.DriverClauses[driver]
	if !requirementHasClause(p, requirementRow, requirementID, clauseID) {
		return "", false
	}
	outcomeID := batch.OutcomeIDs[row]
	clauseRow := int(clauseID - 1)
	explanationID := batch.DriverExplanations[driver]
	reasonID := batch.DriverReasons[driver]
	if reasonID == 0 {
		branch := clauseRow * clauseExplanationBranchCount
		satisfied := outcomeID == p.ClauseOnSatisfied[clauseRow] && explanationID == p.ClauseExplanationIDs[branch]
		conditionFalse := outcomeID == p.ClauseOnFalse[clauseRow] && explanationID == p.ClauseExplanationIDs[branch+1]
		if satisfied == conditionFalse {
			return "", false
		}
		if satisfied {
			return "satisfied", true
		}
		return "condition_false", true
	}
	name, ok := result.ReasonName(reasonID)
	if !ok {
		return "", false
	}
	resolutionRow := clauseRow*truth.ReasonCount + int(reasonID-1)
	if p.Resolutions.OutcomeIDs[resolutionRow] != outcomeID || p.Resolutions.ExplanationIDs[resolutionRow] != explanationID {
		return "", false
	}
	return name, true
}

func requirementHasClause(p *program.Program, row uint32, requirementID schema.RequirementID, clauseID schema.ClauseID) bool {
	// row comes from an Explainer bound to this Program's RequirementIDs order.
	if requirementID == 0 || clauseID == 0 || uint64(clauseID) > uint64(len(p.ClauseAssertionRoots)) ||
		uint64(row) >= uint64(len(p.RequirementIDs)) || p.RequirementIDs[row] != requirementID {
		return false
	}
	start := uint64(p.RequirementClauseStarts[row])
	end := start + uint64(p.RequirementClauseCounts[row])
	for _, candidate := range p.RequirementClauseIDs[int(start):int(end)] {
		if candidate == clauseID {
			return true
		}
	}
	return false
}

func appendRow(
	dst []byte,
	requestID schema.RequestID,
	outcome []byte,
	reason string,
	batch *result.Batch,
	row uint32,
	materialized *result.Materialized,
) ([]byte, bool) {
	driver := batch.DriverOffsets[row]
	dst = append(dst, "    {\n      \"request_id\": "...)
	dst = appendQuotedPrefixedID(dst, 'R', uint32(requestID))
	dst = append(dst, ",\n      \"decision\": "...)
	dst = appendJSONString(dst, outcome)
	dst = append(dst, ",\n      \"rationale\": "...)
	rationale, ok := materializedText(materialized, materialized.Rationale)
	if !ok {
		return dst, false
	}
	dst = appendJSONString(dst, rationale)
	dst = append(dst, ",\n      \"driver\": {\n        \"requirement_id\": "...)
	dst = appendQuotedPrefixedID(dst, 'R', uint32(batch.DriverRequirements[driver]))
	dst = append(dst, ",\n        \"clause_id\": "...)
	dst = appendQuotedPrefixedID(dst, 'C', uint32(batch.DriverClauses[driver]))
	dst = append(dst, ",\n        \"reason\": \""...)
	dst = append(dst, reason...)
	dst = append(dst, "\"\n      },\n      \"requirements_applied\": "...)
	dst = appendIDArray(dst, 'R', materialized.Requirements)
	dst = append(dst, ",\n      \"evidence_used\": "...)
	dst = appendIDArray(dst, 'E', materialized.Evidence)
	dst = append(dst, ",\n      \"missing_or_conflicting_evidence\": "...)
	dst, ok = appendTextArray(dst, materialized, materialized.EvidenceIssues)
	if !ok {
		return dst, false
	}
	dst = append(dst, ",\n      \"assumptions\": "...)
	dst, ok = appendTextArray(dst, materialized, materialized.Assumptions)
	if !ok {
		return dst, false
	}
	dst = append(dst, ",\n      \"unresolved_uncertainty\": "...)
	dst, ok = appendTextArray(dst, materialized, materialized.Uncertainty)
	if !ok {
		return dst, false
	}
	dst = append(dst, ",\n      \"remediation\": "...)
	dst, ok = appendRemediationArray(dst, materialized)
	if !ok {
		return dst, false
	}
	return append(dst, "\n    }"...), true
}

func appendIDArray[T ~uint32](dst []byte, prefix byte, ids []T) []byte {
	if len(ids) == 0 {
		return append(dst, '[', ']')
	}
	dst = append(dst, '[', '\n')
	for row, id := range ids {
		dst = append(dst, "        "...)
		dst = appendQuotedPrefixedID(dst, prefix, uint32(id))
		if row+1 != len(ids) {
			dst = append(dst, ',')
		}
		dst = append(dst, '\n')
	}
	return append(dst, "      ]"...)
}

func appendTextArray(dst []byte, materialized *result.Materialized, ranges []result.TextRange) ([]byte, bool) {
	if len(ranges) == 0 {
		return append(dst, '[', ']'), true
	}
	dst = append(dst, '[', '\n')
	for row, textRange := range ranges {
		text, ok := materializedText(materialized, textRange)
		if !ok {
			return dst, false
		}
		dst = append(dst, "        "...)
		dst = appendJSONString(dst, text)
		if row+1 != len(ranges) {
			dst = append(dst, ',')
		}
		dst = append(dst, '\n')
	}
	return append(dst, "      ]"...), true
}

func appendRemediationArray(dst []byte, materialized *result.Materialized) ([]byte, bool) {
	if len(materialized.Remediations) == 0 {
		return append(dst, '[', ']'), true
	}
	dst = append(dst, '[', '\n')
	for row, remediation := range materialized.Remediations {
		dst = append(dst, "        {\n"...)
		switch remediation.Kind {
		case result.RemediationSetField:
			field, ok := materializedText(materialized, remediation.FieldName)
			if !ok {
				return dst, false
			}
			value, ok := materializedText(materialized, remediation.Value)
			if !ok {
				return dst, false
			}
			dst = append(dst, "          \"action\": \"set_field\",\n          \"field\": "...)
			dst = appendJSONString(dst, field)
			dst = append(dst, ",\n          \"value\": "...)
			switch remediation.ValueKind {
			case schema.ValueKindSymbol:
				dst = appendJSONString(dst, value)
			case schema.ValueKindInteger, schema.ValueKindBoolean, schema.ValueKindTimestamp:
				dst = append(dst, value...)
			default:
				return dst, false
			}
		case result.RemediationAddEvidence:
			kind, ok := materializedText(materialized, remediation.EvidenceKindName)
			if !ok {
				return dst, false
			}
			dst = append(dst, "          \"action\": \"add_evidence\",\n          \"evidence_kind\": "...)
			dst = appendJSONString(dst, kind)
		default:
			return dst, false
		}
		dst = append(dst, "\n        }"...)
		if row+1 != len(materialized.Remediations) {
			dst = append(dst, ',')
		}
		dst = append(dst, '\n')
	}
	return append(dst, "      ]"...), true
}

func materializedText(materialized *result.Materialized, text result.TextRange) ([]byte, bool) {
	if materialized == nil || text.Start > text.End || uint64(text.End) > uint64(len(materialized.Bytes)) {
		return nil, false
	}
	return materialized.Bytes[int(text.Start):int(text.End)], true
}

func appendJSONString(dst, value []byte) []byte {
	return wire.AppendJSONString(dst, value)
}

func appendPrefixedID(dst []byte, prefix byte, id uint32) []byte {
	dst = append(dst, prefix)
	return strconv.AppendUint(dst, uint64(id), 10)
}

func appendQuotedPrefixedID(dst []byte, prefix byte, id uint32) []byte {
	dst = append(dst, '"')
	dst = appendPrefixedID(dst, prefix, id)
	return append(dst, '"')
}

func appendHash(dst []byte, hash [32]byte) []byte {
	return wire.AppendSHA256(dst, hash)
}
