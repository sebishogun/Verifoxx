package jsonpolicy

import (
	"bytes"
	"errors"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

var (
	keyOutcome               = []byte("outcome")
	keyExplanation           = []byte("explanation")
	keyRationale             = []byte("rationale")
	keyUncertainty           = []byte("uncertainty")
	keyIssue                 = []byte("issue")
	keyWrongScope            = []byte("wrong_scope")
	keyWrongSubject          = []byte("wrong_subject")
	keyWrongTiming           = []byte("wrong_timing")
	keyInvalidEvidenceReason = []byte("invalid")
)

func (d *decoder) decodeTemplate(dst *ast.Builder, context ast.TemplateContext) (schema.TemplateID, error) {
	start := d.pos
	value, err := d.expectString(&d.valueScratch)
	if err != nil {
		return 0, err
	}
	if d.limits.MaxTemplateBytes > 0 && len(value) > d.limits.MaxTemplateBytes {
		return 0, d.failAt(CodeLimit, start, "template exceeds MaxTemplateBytes")
	}
	id, err := dst.AddTemplate(value, context)
	if err == nil {
		return id, nil
	}
	if errors.Is(err, ast.ErrTemplateTooLarge) || errors.Is(err, ast.ErrTooManyTemplates) {
		return 0, d.failAt(CodeLimit, start, err.Error())
	}
	return 0, d.failAt(CodeMalformed, start, err.Error())
}

func (d *decoder) decodeAssumptions(dst *ast.Builder) error {
	if err := d.expectArrayStart(); err != nil {
		return err
	}
	var ids [ast.MaxAssumptions]schema.TemplateID
	count := 0
	d.skipWS()
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input in assumptions array")
	}
	if d.src[d.pos] == ']' {
		d.pos++
		return dst.SetAssumptions(nil)
	}
	for {
		if count >= ast.MaxAssumptions || d.limits.MaxAssumptions > 0 && count >= d.limits.MaxAssumptions {
			return d.fail(CodeLimit, "assumptions exceed limit")
		}
		id, err := d.decodeTemplate(dst, ast.TemplateContextAssumption)
		if err != nil {
			return err
		}
		ids[count] = id
		count++
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in assumptions array")
		}
		switch d.src[d.pos] {
		case ',':
			d.pos++
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in assumptions array")
			}
			if d.src[d.pos] == ']' {
				return d.fail(CodeMalformed, "trailing comma in assumptions array")
			}
		case ']':
			d.pos++
			if err := dst.SetAssumptions(ids[:count]); err != nil {
				return d.builderError(err)
			}
			return nil
		default:
			return d.fail(CodeMalformed, "expected ',' or ']' in assumptions array")
		}
	}
}

func explanationKeyIndex(key []byte) int {
	switch {
	case bytes.Equal(key, keyRationale):
		return 0
	case bytes.Equal(key, keyUncertainty):
		return 1
	}
	return -1
}

func resolutionBranchKeyIndex(key []byte) int {
	switch {
	case bytes.Equal(key, keyOutcome):
		return 0
	case bytes.Equal(key, keyExplanation):
		return 1
	}
	return -1
}

func (d *decoder) decodeResolutionBranch(dst *ast.Builder, branch int) (schema.OutcomeID, schema.ExplanationID, error) {
	objectStart := d.pos
	if d.atEOF() || d.src[d.pos] != '{' {
		return 0, 0, d.fail(CodeInvalidType, "expected a resolution branch object")
	}
	d.pos++
	var outcome schema.OutcomeID
	var explanation schema.ExplanationID
	var seen uint8
	requireKey := false
	for {
		d.skipWS()
		if d.atEOF() {
			return 0, 0, d.fail(CodeTruncated, "unexpected end of input in resolution branch")
		}
		if d.src[d.pos] == '}' {
			if requireKey {
				return 0, 0, d.fail(CodeMalformed, "trailing comma in resolution branch")
			}
			d.pos++
			break
		}
		if d.src[d.pos] != '"' {
			return 0, 0, d.fail(CodeMalformed, "expected an object key")
		}
		requireKey = false
		keyStart := d.pos
		key, err := d.parseString(&d.keyScratch)
		if err != nil {
			return 0, 0, err
		}
		index := resolutionBranchKeyIndex(key)
		if index < 0 {
			return 0, 0, d.failAt(CodeUnknownKey, keyStart, "unknown key in resolution branch")
		}
		bit := uint8(1 << uint(index))
		if seen&bit != 0 {
			return 0, 0, d.failAt(CodeDuplicateKey, keyStart, "duplicate key in resolution branch")
		}
		seen |= bit
		d.skipWS()
		if err := d.expectPunct(':'); err != nil {
			return 0, 0, err
		}
		d.skipWS()
		switch index {
		case 0:
			valueStart := d.pos
			value, err := d.expectString(&d.valueScratch)
			if err != nil {
				return 0, 0, err
			}
			outcome, err = d.resolveOutcome(dst, value, valueStart)
			if err != nil {
				return 0, 0, err
			}
		case 1:
			context := ast.TemplateContextUnresolved
			if branch < 2 {
				context = ast.TemplateContextDecision
			}
			explanation, err = d.decodeDecisionExplanation(dst, context)
			if err != nil {
				return 0, 0, err
			}
		}
		d.skipWS()
		if d.atEOF() {
			return 0, 0, d.fail(CodeTruncated, "unexpected end of input in resolution branch")
		}
		if d.src[d.pos] == ',' {
			d.pos++
			requireKey = true
			continue
		}
		if d.src[d.pos] != '}' {
			return 0, 0, d.fail(CodeMalformed, "expected ',' or '}' in resolution branch")
		}
	}
	if seen != 0x3 {
		return 0, 0, d.failAt(CodeMissingKey, objectStart, "resolution branch missing a required key")
	}
	return outcome, explanation, nil
}

func (d *decoder) decodeDecisionExplanation(dst *ast.Builder, context ast.TemplateContext) (schema.ExplanationID, error) {
	objectStart := d.pos
	if d.atEOF() || d.src[d.pos] != '{' {
		return 0, d.fail(CodeInvalidType, "expected an explanation object")
	}
	d.pos++
	var rationale schema.TemplateID
	var uncertainty [ast.MaxUncertainty]schema.TemplateID
	uncertaintyCount := 0
	var seen uint8
	requireKey := false
	for {
		d.skipWS()
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input in explanation object")
		}
		if d.src[d.pos] == '}' {
			if requireKey {
				return 0, d.fail(CodeMalformed, "trailing comma in explanation object")
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
		index := explanationKeyIndex(key)
		if index < 0 {
			return 0, d.failAt(CodeUnknownKey, keyStart, "unknown key in explanation object")
		}
		bit := uint8(1 << uint(index))
		if seen&bit != 0 {
			return 0, d.failAt(CodeDuplicateKey, keyStart, "duplicate key in explanation object")
		}
		seen |= bit
		d.skipWS()
		if err := d.expectPunct(':'); err != nil {
			return 0, err
		}
		d.skipWS()
		switch index {
		case 0:
			rationale, err = d.decodeTemplate(dst, context)
			if err != nil {
				return 0, err
			}
		case 1:
			uncertaintyCount, err = d.decodeUncertainty(dst, context, &uncertainty)
			if err != nil {
				return 0, err
			}
		}
		d.skipWS()
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input in explanation object")
		}
		if d.src[d.pos] == ',' {
			d.pos++
			requireKey = true
			continue
		}
		if d.src[d.pos] != '}' {
			return 0, d.fail(CodeMalformed, "expected ',' or '}' in explanation object")
		}
	}
	if seen != 0x3 {
		return 0, d.failAt(CodeMissingKey, objectStart, "explanation object missing a required key")
	}
	id, err := dst.AddExplanation(rationale, uncertainty[:uncertaintyCount])
	if err != nil {
		return 0, d.builderError(err)
	}
	return id, nil
}

func (d *decoder) decodeUncertainty(dst *ast.Builder, context ast.TemplateContext, ids *[ast.MaxUncertainty]schema.TemplateID) (int, error) {
	if err := d.expectArrayStart(); err != nil {
		return 0, err
	}
	d.skipWS()
	if d.atEOF() {
		return 0, d.fail(CodeTruncated, "unexpected end of input in uncertainty array")
	}
	if d.src[d.pos] == ']' {
		d.pos++
		return 0, nil
	}
	count := 0
	for {
		if count >= ast.MaxUncertainty || d.limits.MaxUncertainty > 0 && count >= d.limits.MaxUncertainty {
			return 0, d.fail(CodeLimit, "uncertainty exceeds limit")
		}
		id, err := d.decodeTemplate(dst, context)
		if err != nil {
			return 0, err
		}
		ids[count] = id
		count++
		d.skipWS()
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input in uncertainty array")
		}
		switch d.src[d.pos] {
		case ',':
			d.pos++
			d.skipWS()
			if d.atEOF() {
				return 0, d.fail(CodeTruncated, "unexpected end of input in uncertainty array")
			}
			if d.src[d.pos] == ']' {
				return 0, d.fail(CodeMalformed, "trailing comma in uncertainty array")
			}
		case ']':
			d.pos++
			return count, nil
		default:
			return 0, d.fail(CodeMalformed, "expected ',' or ']' in uncertainty array")
		}
	}
}

func evidenceIssueKeyIndex(key []byte) int {
	switch {
	case bytes.Equal(key, keyIssue):
		return 0
	case bytes.Equal(key, keyMissing):
		return int(ast.EvidenceIssueMissing) + 1
	case bytes.Equal(key, keyStale):
		return int(ast.EvidenceIssueStale) + 1
	case bytes.Equal(key, keyUnclear):
		return int(ast.EvidenceIssueUnclear) + 1
	case bytes.Equal(key, keyUnverifiable):
		return int(ast.EvidenceIssueUnverifiable) + 1
	case bytes.Equal(key, keyWrongScope):
		return int(ast.EvidenceIssueWrongScope) + 1
	case bytes.Equal(key, keyWrongSubject):
		return int(ast.EvidenceIssueWrongSubject) + 1
	case bytes.Equal(key, keyWrongTiming):
		return int(ast.EvidenceIssueWrongTiming) + 1
	case bytes.Equal(key, keyInvalidEvidenceReason):
		return int(ast.EvidenceIssueInvalid) + 1
	case bytes.Equal(key, keyConflict):
		return int(ast.EvidenceIssueConflict) + 1
	}
	return -1
}

func (d *decoder) decodeEvidenceExplanation(dst *ast.Builder) ([ast.EvidenceIssueReasonCount]schema.TemplateID, error) {
	var result [ast.EvidenceIssueReasonCount]schema.TemplateID
	objectStart := d.pos
	if d.atEOF() || d.src[d.pos] != '{' {
		return result, d.fail(CodeInvalidType, "expected an evidence explanation object")
	}
	d.pos++
	var fallback schema.TemplateID
	var seen uint16
	requireKey := false
	for {
		d.skipWS()
		if d.atEOF() {
			return result, d.fail(CodeTruncated, "unexpected end of input in evidence explanation object")
		}
		if d.src[d.pos] == '}' {
			if requireKey {
				return result, d.fail(CodeMalformed, "trailing comma in evidence explanation object")
			}
			d.pos++
			break
		}
		if d.src[d.pos] != '"' {
			return result, d.fail(CodeMalformed, "expected an object key")
		}
		requireKey = false
		keyStart := d.pos
		key, err := d.parseString(&d.keyScratch)
		if err != nil {
			return result, err
		}
		index := evidenceIssueKeyIndex(key)
		if index < 0 {
			return result, d.failAt(CodeUnknownKey, keyStart, "unknown evidence explanation key")
		}
		bit := uint16(1) << uint(index)
		if seen&bit != 0 {
			return result, d.failAt(CodeDuplicateKey, keyStart, "duplicate evidence explanation key")
		}
		seen |= bit
		d.skipWS()
		if err := d.expectPunct(':'); err != nil {
			return result, err
		}
		d.skipWS()
		context := ast.TemplateContextEvidencePresent
		if index == 0 || index == int(ast.EvidenceIssueMissing)+1 {
			context = ast.TemplateContextEvidenceMissing
		}
		id, err := d.decodeTemplate(dst, context)
		if err != nil {
			return result, err
		}
		if index == 0 {
			fallback = id
		} else {
			result[index-1] = id
		}
		d.skipWS()
		if d.atEOF() {
			return result, d.fail(CodeTruncated, "unexpected end of input in evidence explanation object")
		}
		if d.src[d.pos] == ',' {
			d.pos++
			requireKey = true
			continue
		}
		if d.src[d.pos] != '}' {
			return result, d.fail(CodeMalformed, "expected ',' or '}' in evidence explanation object")
		}
	}
	if seen&1 == 0 {
		return result, d.failAt(CodeMissingKey, objectStart, "evidence explanation missing issue")
	}
	for i := range result {
		if result[i] == 0 {
			result[i] = fallback
		}
	}
	return result, nil
}
