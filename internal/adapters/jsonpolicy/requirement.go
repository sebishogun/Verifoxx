package jsonpolicy

import (
	"bytes"
	"math"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

var (
	keyID           = []byte("id")
	keySource       = []byte("source")
	keyApplies      = []byte("applies")
	keyClauses      = []byte("clauses")
	keyAssert       = []byte("assert")
	keyEvidence     = []byte("evidence")
	keyResolution   = []byte("resolution")
	keyRemediations = []byte("remediations")
	keySatisfied    = []byte("satisfied")
	keyFalse        = []byte("false")
	keyMissing      = []byte("missing")
	keyStale        = []byte("stale")
	keyUnclear      = []byte("unclear")
	keyUnverifiable = []byte("unverifiable")
	keyConflict     = []byte("conflict")
	keyEvidenceKind = []byte("evidence_kind")
	keySetField     = []byte("set_field")
	keyAddEvidence  = []byte("add_evidence")
)

const (
	requirementKeyID = iota
	requirementKeySource
	requirementKeyApplies
	requirementKeyClauses
)

func requirementKeyIndex(key []byte) int {
	switch {
	case bytes.Equal(key, keyID):
		return requirementKeyID
	case bytes.Equal(key, keySource):
		return requirementKeySource
	case bytes.Equal(key, keyApplies):
		return requirementKeyApplies
	case bytes.Equal(key, keyClauses):
		return requirementKeyClauses
	}
	return -1
}

const (
	clauseKeyAssert = iota
	clauseKeyEvidence
	clauseKeyResolution
	clauseKeyRemediations
)

func clauseKeyIndex(key []byte) int {
	switch {
	case bytes.Equal(key, keyAssert):
		return clauseKeyAssert
	case bytes.Equal(key, keyEvidence):
		return clauseKeyEvidence
	case bytes.Equal(key, keyResolution):
		return clauseKeyResolution
	case bytes.Equal(key, keyRemediations):
		return clauseKeyRemediations
	}
	return -1
}

func resolutionKeyIndex(key []byte) int {
	switch {
	case bytes.Equal(key, keySatisfied):
		return 0
	case bytes.Equal(key, keyFalse):
		return 1
	case bytes.Equal(key, keyMissing):
		return 2
	case bytes.Equal(key, keyStale):
		return 3
	case bytes.Equal(key, keyUnclear):
		return 4
	case bytes.Equal(key, keyUnverifiable):
		return 5
	case bytes.Equal(key, keyConflict):
		return 6
	}
	return -1
}

const (
	remediationKeyKind = iota
	remediationKeyField
	remediationKeyValue
	remediationKeyEvidenceKind
)

func remediationKeyIndex(key []byte) int {
	switch {
	case bytes.Equal(key, keyKind):
		return remediationKeyKind
	case bytes.Equal(key, keyField):
		return remediationKeyField
	case bytes.Equal(key, keyValue):
		return remediationKeyValue
	case bytes.Equal(key, keyEvidenceKind):
		return remediationKeyEvidenceKind
	}
	return -1
}

// decodeRequirements consumes the root requirements array. Non-empty arrays
// depend on catalogs and outcomes already decoded so all names resolve in one
// pass without a secondary object representation.
func (d *decoder) decodeRequirements(dst *ast.Builder) error {
	if err := d.expectArrayStart(); err != nil {
		return err
	}
	d.skipWS()
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input in requirements array")
	}
	if d.src[d.pos] == ']' {
		d.pos++
		return nil
	}
	const dependencyMask = uint8(1<<4 | 1<<5 | 1<<6)
	if d.saw&dependencyMask != dependencyMask || d.fields == nil || d.symbols == nil {
		return d.fail(CodeInvalidReference, "requirements must follow catalogs and have a field schema")
	}

	count := 0
	for {
		if d.limits.MaxRequirements > 0 && count >= d.limits.MaxRequirements {
			return d.fail(CodeLimit, "requirements exceed MaxRequirements")
		}
		if d.limits.MaxArrayItems > 0 && count >= d.limits.MaxArrayItems {
			return d.fail(CodeLimit, "requirements exceed MaxArrayItems")
		}
		if err := d.decodeRequirement(dst); err != nil {
			return err
		}
		count++
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in requirements array")
		}
		switch d.src[d.pos] {
		case ',':
			d.pos++
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in requirements array")
			}
			if d.src[d.pos] == ']' {
				return d.fail(CodeMalformed, "trailing comma in requirements array")
			}
		case ']':
			d.pos++
			return nil
		default:
			return d.fail(CodeMalformed, "expected ',' or ']' in requirements array")
		}
	}
}

func (d *decoder) decodeRequirement(dst *ast.Builder) error {
	objectStart := d.pos
	if err := d.expectPunct('{'); err != nil {
		return err
	}
	clauseBase := len(d.clauseScratch)
	var id schema.RequirementID
	var idOffset int
	var sourceSpan ast.SourceSpan
	var applies schema.NodeID
	var seen uint8
	requireKey := false

	for {
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in requirement object")
		}
		if d.src[d.pos] == '}' {
			if requireKey {
				return d.fail(CodeMalformed, "trailing comma in requirement object")
			}
			d.pos++
			break
		}
		if d.src[d.pos] != '"' {
			return d.fail(CodeMalformed, "expected an object key")
		}
		requireKey = false
		keyStart := d.pos
		key, err := d.parseString(&d.keyScratch)
		if err != nil {
			return err
		}
		index := requirementKeyIndex(key)
		if index < 0 {
			return d.failAt(CodeUnknownKey, keyStart, "unknown key in requirement object")
		}
		bit := uint8(1 << uint(index))
		if seen&bit != 0 {
			return d.failAt(CodeDuplicateKey, keyStart, "duplicate key in requirement object")
		}
		seen |= bit
		d.skipWS()
		if err := d.expectPunct(':'); err != nil {
			return err
		}
		d.skipWS()
		switch index {
		case requirementKeyID:
			idOffset = d.pos
			value, err := d.expectString(&d.valueScratch)
			if err != nil {
				return err
			}
			id, err = d.parseRequirementID(value, idOffset)
			if err != nil {
				return err
			}
			if err := d.rejectDuplicateRequirementID(dst, id, idOffset); err != nil {
				return err
			}
		case requirementKeySource:
			start := d.pos
			if _, err := d.expectString(&d.valueScratch); err != nil {
				return err
			}
			sourceSpan = ast.SourceSpan{Start: uint32(start), End: uint32(d.pos)}
		case requirementKeyApplies:
			applies, err = d.decodeExpression(dst, 1)
			if err != nil {
				return err
			}
		case requirementKeyClauses:
			if err := d.decodeClauseArray(dst); err != nil {
				return err
			}
		}
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in requirement object")
		}
		if d.src[d.pos] == ',' {
			d.pos++
			requireKey = true
			continue
		}
		if d.src[d.pos] != '}' {
			return d.fail(CodeMalformed, "expected ',' or '}' in requirement object")
		}
	}

	if seen != 0xf {
		return d.failAt(CodeMissingKey, objectStart, "requirement object missing a required key")
	}
	clauses := d.clauseScratch[clauseBase:]
	err := dst.AddRequirement(id, applies, clauses, sourceSpan)
	d.clauseScratch = d.clauseScratch[:clauseBase]
	if err != nil {
		return d.builderError(err)
	}
	return nil
}

// rejectDuplicateRequirementID rejects an id that matches a requirement
// already decoded into the AST, so a duplicate is reported at its id token
// without decoding the rest of the requirement.
func (d *decoder) rejectDuplicateRequirementID(dst *ast.Builder, id schema.RequirementID, offset int) error {
	for _, existing := range dst.Document().RequirementIDs {
		if existing == id {
			return d.failAt(CodeDuplicateID, offset, "duplicate requirement ID")
		}
	}
	return nil
}

func (d *decoder) parseRequirementID(value []byte, offset int) (schema.RequirementID, error) {
	if len(value) < 2 || value[0] != 'R' || value[1] == '0' {
		return 0, d.failAt(CodeMalformed, offset, "requirement ID must be R followed by a non-zero decimal integer")
	}
	var id uint64
	for _, b := range value[1:] {
		if !isDigit(b) {
			return 0, d.failAt(CodeMalformed, offset, "requirement ID must contain only decimal digits")
		}
		digit := uint64(b - '0')
		if id > (uint64(math.MaxUint32)-digit)/10 {
			return 0, d.failAt(CodeLimit, offset, "requirement ID exceeds uint32")
		}
		id = id*10 + digit
	}
	return schema.RequirementID(id), nil
}

func (d *decoder) decodeClauseArray(dst *ast.Builder) error {
	if err := d.expectArrayStart(); err != nil {
		return err
	}
	d.skipWS()
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input in clauses array")
	}
	if d.src[d.pos] == ']' {
		d.pos++
		return d.fail(CodeInvalidArity, "clauses array must not be empty")
	}
	count := 0
	for {
		if d.limits.MaxClauses > 0 && len(dst.Document().ClauseAssertionRoots) >= d.limits.MaxClauses {
			return d.fail(CodeLimit, "clauses exceed MaxClauses")
		}
		if d.limits.MaxArrayItems > 0 && count >= d.limits.MaxArrayItems {
			return d.fail(CodeLimit, "clauses exceed MaxArrayItems")
		}
		id, err := d.decodeClause(dst)
		if err != nil {
			return err
		}
		d.clauseScratch = append(d.clauseScratch, id)
		count++
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in clauses array")
		}
		switch d.src[d.pos] {
		case ',':
			d.pos++
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in clauses array")
			}
			if d.src[d.pos] == ']' {
				return d.fail(CodeMalformed, "trailing comma in clauses array")
			}
		case ']':
			d.pos++
			return nil
		default:
			return d.fail(CodeMalformed, "expected ',' or ']' in clauses array")
		}
	}
}

func (d *decoder) decodeClause(dst *ast.Builder) (schema.ClauseID, error) {
	objectStart := d.pos
	if err := d.expectPunct('{'); err != nil {
		return 0, err
	}
	evidenceBase := len(d.nodeScratch)
	remedyBase := len(d.remedyScratch)
	var assertion schema.NodeID
	var resolution ast.Resolution
	var seen uint8
	requireKey := false

	for {
		d.skipWS()
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input in clause object")
		}
		if d.src[d.pos] == '}' {
			if requireKey {
				return 0, d.fail(CodeMalformed, "trailing comma in clause object")
			}
			d.pos++
			break
		}
		if d.src[d.pos] != '"' {
			return 0, d.fail(CodeMalformed, "expected an object key")
		}
		requireKey = false
		keyStart := d.pos
		key, err := d.parseString(&d.keyScratch)
		if err != nil {
			return 0, err
		}
		index := clauseKeyIndex(key)
		if index < 0 {
			return 0, d.failAt(CodeUnknownKey, keyStart, "unknown key in clause object")
		}
		bit := uint8(1 << uint(index))
		if seen&bit != 0 {
			return 0, d.failAt(CodeDuplicateKey, keyStart, "duplicate key in clause object")
		}
		seen |= bit
		d.skipWS()
		if err := d.expectPunct(':'); err != nil {
			return 0, err
		}
		d.skipWS()
		switch index {
		case clauseKeyAssert:
			assertion, err = d.decodeExpression(dst, 1)
			if err != nil {
				return 0, err
			}
		case clauseKeyEvidence:
			if err := d.decodeEvidenceArray(dst); err != nil {
				return 0, err
			}
		case clauseKeyResolution:
			resolution, err = d.decodeResolution(dst)
			if err != nil {
				return 0, err
			}
		case clauseKeyRemediations:
			if err := d.decodeRemediationArray(dst); err != nil {
				return 0, err
			}
		}
		d.skipWS()
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input in clause object")
		}
		if d.src[d.pos] == ',' {
			d.pos++
			requireKey = true
			continue
		}
		if d.src[d.pos] != '}' {
			return 0, d.fail(CodeMalformed, "expected ',' or '}' in clause object")
		}
	}

	if seen != 0xf {
		return 0, d.failAt(CodeMissingKey, objectStart, "clause object missing a required key")
	}
	span := ast.SourceSpan{Start: uint32(objectStart), End: uint32(d.pos)}
	id, err := dst.AddClause(assertion, d.nodeScratch[evidenceBase:], resolution, d.remedyScratch[remedyBase:], span)
	d.nodeScratch = d.nodeScratch[:evidenceBase]
	d.remedyScratch = d.remedyScratch[:remedyBase]
	if err != nil {
		return 0, d.builderError(err)
	}
	return id, nil
}

func (d *decoder) decodeEvidenceArray(dst *ast.Builder) error {
	if err := d.expectArrayStart(); err != nil {
		return err
	}
	d.skipWS()
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input in evidence array")
	}
	if d.src[d.pos] == ']' {
		d.pos++
		return nil
	}
	count := 0
	for {
		if d.limits.MaxArrayItems > 0 && count >= d.limits.MaxArrayItems {
			return d.fail(CodeLimit, "evidence exceeds MaxArrayItems")
		}
		id, err := d.decodeExpression(dst, 1)
		if err != nil {
			return err
		}
		kind, ok := dst.Document().Kind(id)
		if !ok || kind != ast.NodeKindEvidence {
			return d.fail(CodeInvalidType, "clause evidence must use evidence_matches")
		}
		d.nodeScratch = append(d.nodeScratch, id)
		count++
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in evidence array")
		}
		switch d.src[d.pos] {
		case ',':
			d.pos++
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in evidence array")
			}
			if d.src[d.pos] == ']' {
				return d.fail(CodeMalformed, "trailing comma in evidence array")
			}
		case ']':
			d.pos++
			return nil
		default:
			return d.fail(CodeMalformed, "expected ',' or ']' in evidence array")
		}
	}
}

func (d *decoder) decodeResolution(dst *ast.Builder) (ast.Resolution, error) {
	objectStart := d.pos
	if err := d.expectPunct('{'); err != nil {
		return ast.Resolution{}, err
	}
	var result ast.Resolution
	var seen uint8
	requireKey := false
	for {
		d.skipWS()
		if d.atEOF() {
			return ast.Resolution{}, d.fail(CodeTruncated, "unexpected end of input in resolution object")
		}
		if d.src[d.pos] == '}' {
			if requireKey {
				return ast.Resolution{}, d.fail(CodeMalformed, "trailing comma in resolution object")
			}
			d.pos++
			break
		}
		if d.src[d.pos] != '"' {
			return ast.Resolution{}, d.fail(CodeMalformed, "expected an object key")
		}
		requireKey = false
		keyStart := d.pos
		key, err := d.parseString(&d.keyScratch)
		if err != nil {
			return ast.Resolution{}, err
		}
		index := resolutionKeyIndex(key)
		if index < 0 {
			return ast.Resolution{}, d.failAt(CodeUnknownKey, keyStart, "unknown key in resolution object")
		}
		bit := uint8(1 << uint(index))
		if seen&bit != 0 {
			return ast.Resolution{}, d.failAt(CodeDuplicateKey, keyStart, "duplicate key in resolution object")
		}
		seen |= bit
		d.skipWS()
		if err := d.expectPunct(':'); err != nil {
			return ast.Resolution{}, err
		}
		d.skipWS()
		outcome, explanation, err := d.decodeResolutionBranch(dst, index)
		if err != nil {
			return ast.Resolution{}, err
		}
		switch index {
		case 0:
			result.OnSatisfied = outcome
			result.OnSatisfiedExplanation = explanation
		case 1:
			result.OnFalse = outcome
			result.OnFalseExplanation = explanation
		case 2:
			result.OnMissing = outcome
			result.OnMissingExplanation = explanation
		case 3:
			result.OnStale = outcome
			result.OnStaleExplanation = explanation
		case 4:
			result.OnUnclear = outcome
			result.OnUnclearExplanation = explanation
		case 5:
			result.OnUnverifiable = outcome
			result.OnUnverifiableExplanation = explanation
		case 6:
			result.OnConflict = outcome
			result.OnConflictExplanation = explanation
		}
		d.skipWS()
		if d.atEOF() {
			return ast.Resolution{}, d.fail(CodeTruncated, "unexpected end of input in resolution object")
		}
		if d.src[d.pos] == ',' {
			d.pos++
			requireKey = true
			continue
		}
		if d.src[d.pos] != '}' {
			return ast.Resolution{}, d.fail(CodeMalformed, "expected ',' or '}' in resolution object")
		}
	}
	if seen != 0x7f {
		return ast.Resolution{}, d.failAt(CodeMissingKey, objectStart, "resolution object missing a required key")
	}
	return result, nil
}

func (d *decoder) resolveOutcome(dst *ast.Builder, name []byte, offset int) (schema.OutcomeID, error) {
	doc := dst.Document()
	if row, ok := findCatalogName(doc, doc.OutcomeNames, name); ok {
		return schema.OutcomeID(row), nil
	}
	return 0, d.failAt(CodeInvalidReference, offset, "unknown outcome")
}

func (d *decoder) decodeRemediationArray(dst *ast.Builder) error {
	if err := d.expectArrayStart(); err != nil {
		return err
	}
	d.skipWS()
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input in remediations array")
	}
	if d.src[d.pos] == ']' {
		d.pos++
		return nil
	}
	count := 0
	for {
		if d.limits.MaxArrayItems > 0 && count >= d.limits.MaxArrayItems {
			return d.fail(CodeLimit, "remediations exceed MaxArrayItems")
		}
		id, err := d.decodeRemediation(dst)
		if err != nil {
			return err
		}
		d.remedyScratch = append(d.remedyScratch, id)
		count++
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in remediations array")
		}
		switch d.src[d.pos] {
		case ',':
			d.pos++
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in remediations array")
			}
			if d.src[d.pos] == ']' {
				return d.fail(CodeMalformed, "trailing comma in remediations array")
			}
		case ']':
			d.pos++
			return nil
		default:
			return d.fail(CodeMalformed, "expected ',' or ']' in remediations array")
		}
	}
}

type remediationDecodeKind uint8

const (
	remediationDecodeInvalid remediationDecodeKind = iota
	remediationDecodeSetField
	remediationDecodeAddEvidence
)

func (d *decoder) decodeRemediation(dst *ast.Builder) (schema.RemediationID, error) {
	objectStart := d.pos
	if err := d.expectPunct('{'); err != nil {
		return 0, err
	}
	var kind remediationDecodeKind
	var fieldToken ast.SourceSpan
	var valueToken ast.SourceSpan
	var evidenceToken ast.SourceSpan
	var seen uint8
	requireKey := false

	for {
		d.skipWS()
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input in remediation object")
		}
		if d.src[d.pos] == '}' {
			if requireKey {
				return 0, d.fail(CodeMalformed, "trailing comma in remediation object")
			}
			d.pos++
			break
		}
		if d.src[d.pos] != '"' {
			return 0, d.fail(CodeMalformed, "expected an object key")
		}
		requireKey = false
		keyStart := d.pos
		key, err := d.parseString(&d.keyScratch)
		if err != nil {
			return 0, err
		}
		index := remediationKeyIndex(key)
		if index < 0 {
			return 0, d.failAt(CodeUnknownKey, keyStart, "unknown key in remediation object")
		}
		bit := uint8(1 << uint(index))
		if seen&bit != 0 {
			return 0, d.failAt(CodeDuplicateKey, keyStart, "duplicate key in remediation object")
		}
		seen |= bit
		d.skipWS()
		if err := d.expectPunct(':'); err != nil {
			return 0, err
		}
		d.skipWS()
		switch index {
		case remediationKeyKind:
			start := d.pos
			value, err := d.expectString(&d.valueScratch)
			if err != nil {
				return 0, err
			}
			switch {
			case bytes.Equal(value, keySetField):
				kind = remediationDecodeSetField
			case bytes.Equal(value, keyAddEvidence):
				kind = remediationDecodeAddEvidence
			default:
				return 0, d.failAt(CodeMalformed, start, "unknown remediation kind")
			}
		case remediationKeyField:
			start := d.pos
			if _, err := d.expectString(&d.valueScratch); err != nil {
				return 0, err
			}
			fieldToken = ast.SourceSpan{Start: uint32(start), End: uint32(d.pos)}
		case remediationKeyValue:
			start := d.pos
			if err := d.skipScalar(); err != nil {
				return 0, err
			}
			valueToken = ast.SourceSpan{Start: uint32(start), End: uint32(d.pos)}
		case remediationKeyEvidenceKind:
			start := d.pos
			if _, err := d.expectString(&d.valueScratch); err != nil {
				return 0, err
			}
			evidenceToken = ast.SourceSpan{Start: uint32(start), End: uint32(d.pos)}
		}
		d.skipWS()
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input in remediation object")
		}
		if d.src[d.pos] == ',' {
			d.pos++
			requireKey = true
			continue
		}
		if d.src[d.pos] != '}' {
			return 0, d.fail(CodeMalformed, "expected ',' or '}' in remediation object")
		}
	}

	span := ast.SourceSpan{Start: uint32(objectStart), End: uint32(d.pos)}
	switch kind {
	case remediationDecodeSetField:
		if seen != 0x7 {
			return 0, d.failAt(CodeInvalidArity, objectStart, "set_field remediation requires kind, field, and value")
		}
		fieldID, err := d.resolveFieldToken(fieldToken)
		if err != nil {
			return 0, err
		}
		fieldKind, ok := d.fields.Kind(fieldID)
		if !ok {
			return 0, d.failAt(CodeInvalidReference, int(valueToken.Start), "field missing from schema")
		}
		valueID, err := d.decodeValueToken(dst, int(valueToken.Start), int(valueToken.End), fieldKind)
		if err != nil {
			return 0, err
		}
		id, err := dst.AddSetFieldRemediation(fieldID, valueID, span)
		if err != nil {
			return 0, d.builderError(err)
		}
		return id, nil
	case remediationDecodeAddEvidence:
		if seen != 0x9 {
			return 0, d.failAt(CodeInvalidArity, objectStart, "add_evidence remediation requires kind and evidence_kind")
		}
		kindID, err := d.resolveKindToken(dst, evidenceToken)
		if err != nil {
			return 0, err
		}
		id, err := dst.AddEvidenceRemediation(kindID, span)
		if err != nil {
			return 0, d.builderError(err)
		}
		return id, nil
	default:
		return 0, d.failAt(CodeMissingKey, objectStart, "remediation object missing kind")
	}
}

// resolveFieldToken re-parses a recorded field token range and resolves it
// against the field schema. The main scanner position is preserved.
func (d *decoder) resolveFieldToken(token ast.SourceSpan) (schema.FieldID, error) {
	saved := d.pos
	d.pos = int(token.Start)
	v, err := d.expectString(&d.valueScratch)
	var id schema.FieldID
	if err == nil {
		id, err = d.resolveField(v, int(token.Start))
	}
	d.pos = saved
	return id, err
}

func (d *decoder) resolveField(name []byte, offset int) (schema.FieldID, error) {
	if d.fields == nil || d.symbols == nil {
		return 0, d.failAt(CodeInvalidReference, offset, "field schema is unavailable")
	}
	symbolID, ok := d.symbols.Lookup(name)
	if !ok {
		return 0, d.failAt(CodeInvalidReference, offset, "unknown field")
	}
	fieldID, ok := d.fields.Lookup(symbolID)
	if !ok {
		return 0, d.failAt(CodeInvalidReference, offset, "unknown field")
	}
	return fieldID, nil
}
