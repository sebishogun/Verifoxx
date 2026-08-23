// Package jsonpolicy decodes Verifoxx policy documents directly into the
// pointerless AST. It uses a hand-written byte scanner: no encoding/json,
// maps, reflection, or per-token strings. Nested relationships accumulate in
// flat reusable scratch owned by the decoder.
package jsonpolicy

import (
	"bytes"
	"errors"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// Limits bounds one Decode call. A zero value disables every limit.
type Limits struct {
	// MaxSourceBytes is the longest accepted source, checked before decoding.
	MaxSourceBytes int
	// MaxCatalogItems is the maximum entries per catalog and outcome array.
	MaxCatalogItems int
	// MaxStringBytes is the maximum decoded length of any string, including keys.
	MaxStringBytes int
	// MaxDepth is the maximum expression nesting depth. The root expression
	// counts as depth 1.
	MaxDepth int
	// MaxNodes is the maximum AST nodes appended by one decode.
	MaxNodes int
	// MaxValues is the maximum literal values appended by one decode.
	MaxValues int
	// MaxArrayItems is the maximum elements per args and values array.
	MaxArrayItems int
	// MaxSymbolBytes is the maximum decoded symbol bytes copied to the AST slab.
	MaxSymbolBytes int
	// MaxRequirements is the maximum requirements appended by one decode.
	MaxRequirements int
	// MaxClauses is the maximum clauses appended by one decode.
	MaxClauses int
	// MaxTemplateBytes may tighten the AST's decoded-template byte limit.
	MaxTemplateBytes int
	// MaxAssumptions may tighten the AST's policy-assumption limit.
	MaxAssumptions int
	// MaxUncertainty may tighten the AST's uncertainty-per-explanation limit.
	MaxUncertainty int
}

// rootKeys maps decoded root object keys to a stable bit. Order of appearance
// in a document is free; presence of every key is required.
var rootKeys = [...]struct {
	name []byte
}{
	{name: []byte("schema_version")},
	{name: []byte("name")},
	{name: []byte("version")},
	{name: []byte("assumptions")},
	{name: []byte("evidence_kinds")},
	{name: []byte("evidence_states")},
	{name: []byte("outcomes")},
	{name: []byte("requirements")},
}

const allRootKeys = 1<<len(rootKeys) - 1

var (
	keyName       = []byte("name")
	keyPrecedence = []byte("precedence")
	keyTerminal   = []byte("terminal")
)

// outcomeKeyIndex maps a decoded outcome-object key to its bit position.
func outcomeKeyIndex(key []byte) int {
	switch {
	case bytes.Equal(key, keyName):
		return 0
	case bytes.Equal(key, keyPrecedence):
		return 1
	case bytes.Equal(key, keyTerminal):
		return 2
	}
	return -1
}

// Decoder is a reusable decode worker. It owns all scanner state and scratch
// buffers between calls, so warm calls allocate nothing beyond the AST.
// A Decoder is not safe for concurrent use by multiple goroutines.
type Decoder struct {
	decoder
}

// Decode parses one policy document into dst. The builder is reset on entry
// and again on any error, so a failed call always leaves dst empty. fields
// resolves expression field references; symbols resolves field names by
// look-up only and is never mutated by decoding. Root metadata and catalog
// strings are copied into the AST symbol slab, where compilation interns
// them later. The caller's source, fields, and symbols are not retained
// after the call returns.
func (dec *Decoder) Decode(dst *ast.Builder, source []byte, fields *schema.Schema, symbols *schema.Interner, limits Limits) error {
	d := &dec.decoder
	d.src = source
	d.pos = 0
	d.saw = 0
	d.limits = limits
	d.fields = fields
	d.symbols = symbols
	d.keyScratch = d.keyScratch[:0]
	d.valueScratch = d.valueScratch[:0]
	d.nodeScratch = d.nodeScratch[:0]
	d.valueIDScratch = d.valueIDScratch[:0]
	d.clauseScratch = d.clauseScratch[:0]
	d.remedyScratch = d.remedyScratch[:0]

	dst.Reset()
	if limits.MaxSourceBytes > 0 && len(source) > limits.MaxSourceBytes {
		err := &Error{Code: CodeLimit, Offset: limits.MaxSourceBytes, Message: "source exceeds MaxSourceBytes"}
		d.clear()
		return err
	}
	if err := dst.SetSource(source); err != nil {
		derr := d.builderError(err)
		d.clear()
		return derr
	}
	if err := d.decodeRoot(dst); err != nil {
		dst.Reset()
		d.clear()
		return err
	}
	d.clear()
	return nil
}

// clear drops the references to the caller's source, schema, and interner
// and truncates every scratch buffer to zero length while retaining
// capacity, so a Decoder retains nothing from the previous call.
func (d *decoder) clear() {
	d.src = nil
	d.fields = nil
	d.symbols = nil
	d.keyScratch = d.keyScratch[:0]
	d.valueScratch = d.valueScratch[:0]
	d.nodeScratch = d.nodeScratch[:0]
	d.valueIDScratch = d.valueIDScratch[:0]
	d.clauseScratch = d.clauseScratch[:0]
	d.remedyScratch = d.remedyScratch[:0]
}

// Decode parses one policy document into dst. It is equivalent to a single
// call on a fresh zero-value Decoder: scratch capacity never carries across
// calls, so reuse callers should keep a Decoder instead. The builder is
// reset on entry and again on any error, so a failed call always leaves dst
// empty. fields resolves expression field references; symbols resolves field
// names by look-up only and is never mutated by decoding. Root metadata and
// catalog strings are copied into the AST symbol slab, where compilation
// interns them later.
func Decode(dst *ast.Builder, source []byte, fields *schema.Schema, symbols *schema.Interner, limits Limits) error {
	var dec Decoder
	return dec.Decode(dst, source, fields, symbols, limits)
}

// decodeRoot consumes the top-level object, its eight required keys, and the
// end-of-input check.
func (d *decoder) decodeRoot(dst *ast.Builder) error {
	var nameValue, versionValue schema.ValueID
	d.skipWS()
	if err := d.expectPunct('{'); err != nil {
		return err
	}
	requireKey := false
	for {
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in root object")
		}
		if d.src[d.pos] == '}' {
			if requireKey {
				return d.fail(CodeMalformed, "trailing comma in root object")
			}
			d.pos++
			if d.saw != allRootKeys {
				return d.fail(CodeMissingKey, "policy object is missing required keys")
			}
			if err := dst.SetMetadata(nameValue, versionValue); err != nil {
				return d.builderError(err)
			}
			d.skipWS()
			if !d.atEOF() {
				return d.fail(CodeTrailing, "trailing data after policy object")
			}
			return nil
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
		index := -1
		for i := range rootKeys {
			if bytes.Equal(key, rootKeys[i].name) {
				index = i
				break
			}
		}
		if index < 0 {
			return d.failAt(CodeUnknownKey, keyStart, "unknown root key")
		}
		bit := uint8(1 << uint(index))
		if d.saw&bit != 0 {
			return d.failAt(CodeDuplicateKey, keyStart, "duplicate root key")
		}
		d.saw |= bit
		d.skipWS()
		if err := d.expectPunct(':'); err != nil {
			return err
		}
		d.skipWS()
		switch index {
		case 0: // schema_version
			valueStart := d.pos
			v, err := d.expectInteger()
			if err != nil {
				return err
			}
			if v != 1 {
				return d.failAt(CodeInvalidVersion, valueStart, "unsupported schema_version")
			}
		case 1: // name
			value, err := d.expectString(&d.valueScratch)
			if err != nil {
				return err
			}
			nameValue, err = d.addSymbolValue(dst, value)
			if err != nil {
				return err
			}
		case 2: // version
			value, err := d.expectString(&d.valueScratch)
			if err != nil {
				return err
			}
			versionValue, err = d.addSymbolValue(dst, value)
			if err != nil {
				return err
			}
		case 3: // assumptions
			if err := d.decodeAssumptions(dst); err != nil {
				return err
			}
		case 4: // evidence_kinds
			if err := d.decodeEvidenceKinds(dst); err != nil {
				return err
			}
		case 5: // evidence_states
			if err := d.decodeEvidenceStates(dst); err != nil {
				return err
			}
		case 6: // outcomes
			if err := d.decodeOutcomes(dst); err != nil {
				return err
			}
		case 7: // requirements
			if err := d.decodeRequirements(dst); err != nil {
				return err
			}
		}
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in root object")
		}
		if d.src[d.pos] == ',' {
			d.pos++
			requireKey = true
			continue
		}
		if d.src[d.pos] != '}' {
			return d.fail(CodeMalformed, "expected ',' or '}'")
		}
	}
}

// expectArrayStart requires the next value to be an array and consumes '['.
func (d *decoder) expectArrayStart() error {
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input")
	}
	if d.src[d.pos] != '[' {
		return d.fail(CodeInvalidType, "expected a JSON array")
	}
	d.pos++
	return nil
}

// decodeArray consumes '[' ... ']' with one entry per iteration.
func (d *decoder) decodeArray(entry func() error) error {
	if err := d.expectArrayStart(); err != nil {
		return err
	}
	d.skipWS()
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input in array")
	}
	if d.src[d.pos] == ']' {
		d.pos++
		return nil
	}
	count := 0
	for {
		if d.limits.MaxCatalogItems > 0 && count >= d.limits.MaxCatalogItems {
			return d.fail(CodeLimit, "catalog exceeds MaxCatalogItems")
		}
		if err := entry(); err != nil {
			return err
		}
		count++
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in array")
		}
		switch d.src[d.pos] {
		case ',':
			d.pos++
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in array")
			}
			if d.src[d.pos] == ']' {
				return d.fail(CodeMalformed, "trailing comma in array")
			}
		case ']':
			d.pos++
			return nil
		default:
			return d.fail(CodeMalformed, "expected ',' or ']'")
		}
	}
}

func (d *decoder) decodeEvidenceKinds(dst *ast.Builder) error {
	return d.decodeArray(func() error {
		return d.decodeNameEntry(dst, func(id schema.ValueID, span ast.SourceSpan) error {
			if err := d.rejectDuplicateEvidenceKindName(dst, id, int(span.Start)); err != nil {
				return err
			}
			_, err := dst.AddEvidenceKind(id, span)
			return err
		})
	})
}

func (d *decoder) decodeEvidenceStates(dst *ast.Builder) error {
	return d.decodeArray(func() error {
		return d.decodeNameEntry(dst, func(id schema.ValueID, span ast.SourceSpan) error {
			if err := d.rejectDuplicateEvidenceStateName(dst, id, int(span.Start)); err != nil {
				return err
			}
			_, err := dst.AddEvidenceState(id, span)
			return err
		})
	})
}

// rejectDuplicateEvidenceKindName rejects a name that decodes to the same
// bytes as an evidence kind already in the AST.
func (d *decoder) rejectDuplicateEvidenceKindName(dst *ast.Builder, id schema.ValueID, offset int) error {
	name, ok := dst.Document().SymbolValue(id)
	if !ok {
		return d.fail(CodeMalformed, "decoded catalog name is missing")
	}
	doc := dst.Document()
	for i := 1; i <= len(doc.EvidenceKindNames); i++ {
		vid, ok := doc.EvidenceKindName(schema.EvidenceKindID(i))
		if !ok {
			continue
		}
		existing, ok := doc.SymbolValue(vid)
		if !ok {
			continue
		}
		if bytes.Equal(existing, name) {
			return d.failAt(CodeDuplicateID, offset, "duplicate evidence kind name")
		}
	}
	return nil
}

// rejectDuplicateEvidenceStateName rejects a name that decodes to the same
// bytes as an evidence state already in the AST.
func (d *decoder) rejectDuplicateEvidenceStateName(dst *ast.Builder, id schema.ValueID, offset int) error {
	name, ok := dst.Document().SymbolValue(id)
	if !ok {
		return d.fail(CodeMalformed, "decoded catalog name is missing")
	}
	doc := dst.Document()
	for i := 1; i <= len(doc.EvidenceStateNames); i++ {
		vid, ok := doc.EvidenceStateName(schema.EvidenceStateID(i))
		if !ok {
			continue
		}
		existing, ok := doc.SymbolValue(vid)
		if !ok {
			continue
		}
		if bytes.Equal(existing, name) {
			return d.failAt(CodeDuplicateID, offset, "duplicate evidence state name")
		}
	}
	return nil
}

// decodeNameEntry consumes one {"name": string} catalog object.
func (d *decoder) decodeNameEntry(dst *ast.Builder, add func(schema.ValueID, ast.SourceSpan) error) error {
	if err := d.expectPunct('{'); err != nil {
		return err
	}
	var nameID schema.ValueID
	var nameSpan ast.SourceSpan
	sawName := false
	requireKey := false
	for {
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in catalog entry")
		}
		if d.src[d.pos] == '}' {
			if requireKey {
				return d.fail(CodeMalformed, "trailing comma in catalog entry")
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
		if !bytes.Equal(key, keyName) {
			return d.failAt(CodeUnknownKey, keyStart, "unknown key in catalog entry")
		}
		if sawName {
			return d.failAt(CodeDuplicateKey, keyStart, "duplicate key in catalog entry")
		}
		sawName = true
		d.skipWS()
		if err := d.expectPunct(':'); err != nil {
			return err
		}
		d.skipWS()
		valueStart := d.pos
		value, err := d.expectString(&d.valueScratch)
		if err != nil {
			return err
		}
		nameSpan = ast.SourceSpan{Start: uint32(valueStart), End: uint32(d.pos)}
		nameID, err = d.addSymbolValue(dst, value)
		if err != nil {
			return err
		}
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in catalog entry")
		}
		if d.src[d.pos] == ',' {
			d.pos++
			requireKey = true
			continue
		}
		if d.src[d.pos] != '}' {
			return d.fail(CodeMalformed, "expected ',' or '}' in catalog entry")
		}
	}
	if !sawName {
		return d.fail(CodeMissingKey, "catalog entry missing \"name\"")
	}
	return add(nameID, nameSpan)
}

// decodeOutcomes consumes the outcomes array.
func (d *decoder) decodeOutcomes(dst *ast.Builder) error {
	return d.decodeArray(func() error {
		if err := d.expectPunct('{'); err != nil {
			return err
		}
		var seen uint8
		var nameID schema.ValueID
		var nameSpan ast.SourceSpan
		var precedence uint8
		var terminal bool
		requireKey := false
		for {
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in outcome entry")
			}
			if d.src[d.pos] == '}' {
				if requireKey {
					return d.fail(CodeMalformed, "trailing comma in outcome entry")
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
			index := outcomeKeyIndex(key)
			if index < 0 {
				return d.failAt(CodeUnknownKey, keyStart, "unknown key in outcome entry")
			}
			bit := uint8(1 << uint(index))
			if seen&bit != 0 {
				return d.failAt(CodeDuplicateKey, keyStart, "duplicate key in outcome entry")
			}
			seen |= bit
			d.skipWS()
			if err := d.expectPunct(':'); err != nil {
				return err
			}
			d.skipWS()
			switch index {
			case 0: // name
				valueStart := d.pos
				value, err := d.expectString(&d.valueScratch)
				if err != nil {
					return err
				}
				nameSpan = ast.SourceSpan{Start: uint32(valueStart), End: uint32(d.pos)}
				nameID, err = d.addSymbolValue(dst, value)
				if err != nil {
					return err
				}
			case 1: // precedence
				valueStart := d.pos
				v, err := d.expectInteger()
				if err != nil {
					return err
				}
				if v < 0 || v > 255 {
					return d.failAt(CodeLimit, valueStart, "precedence out of uint8 range")
				}
				precedence = uint8(v)
			case 2: // terminal
				value, err := d.expectBool()
				if err != nil {
					return err
				}
				terminal = value
			}
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in outcome entry")
			}
			if d.src[d.pos] == ',' {
				d.pos++
				requireKey = true
				continue
			}
			if d.src[d.pos] != '}' {
				return d.fail(CodeMalformed, "expected ',' or '}' in outcome entry")
			}
		}
		if seen != 0x7 {
			return d.fail(CodeMissingKey, "outcome entry missing a required key")
		}
		if err := d.rejectDuplicateOutcomeName(dst, nameID, int(nameSpan.Start)); err != nil {
			return err
		}
		if _, err := dst.AddOutcome(nameID, precedence, terminal, nameSpan); err != nil {
			return d.builderError(err)
		}
		return nil
	})
}

// rejectDuplicateOutcomeName rejects a name that decodes to the same bytes as
// an outcome already in the AST.
func (d *decoder) rejectDuplicateOutcomeName(dst *ast.Builder, id schema.ValueID, offset int) error {
	name, ok := dst.Document().SymbolValue(id)
	if !ok {
		return d.fail(CodeMalformed, "decoded outcome name is missing")
	}
	doc := dst.Document()
	for i := 1; i <= len(doc.OutcomeNames); i++ {
		vid, _, _, ok := doc.Outcome(schema.OutcomeID(i))
		if !ok {
			continue
		}
		existing, ok := doc.SymbolValue(vid)
		if !ok {
			continue
		}
		if bytes.Equal(existing, name) {
			return d.failAt(CodeDuplicateID, offset, "duplicate outcome name")
		}
	}
	return nil
}

// expectString requires the next value to be a string and decodes it.
func (d *decoder) expectString(scratch *[]byte) ([]byte, error) {
	if d.atEOF() {
		return nil, d.fail(CodeTruncated, "unexpected end of input")
	}
	if d.src[d.pos] != '"' {
		return nil, d.fail(CodeInvalidType, "expected a JSON string")
	}
	return d.parseString(scratch)
}

// expectInteger requires the next value to be an integer and parses it.
func (d *decoder) expectInteger() (int64, error) {
	if d.atEOF() {
		return 0, d.fail(CodeTruncated, "unexpected end of input")
	}
	b := d.src[d.pos]
	if b != '-' && !isDigit(b) {
		return 0, d.fail(CodeInvalidType, "expected a JSON integer")
	}
	return d.parseInteger()
}

// expectBool requires the next value to be a boolean literal.
func (d *decoder) expectBool() (bool, error) {
	if d.atEOF() {
		return false, d.fail(CodeTruncated, "unexpected end of input")
	}
	b := d.src[d.pos]
	if b != 't' && b != 'f' {
		return false, d.fail(CodeInvalidType, "expected a JSON boolean")
	}
	isTrue, _, err := d.parseLiteral()
	if err != nil {
		return false, err
	}
	return isTrue, nil
}

// addSymbolValue copies the decoded string into the document symbol slab.
// Policy symbols belong to AST raw storage; compilation interns them later.
// The scratch the value came from is safe to reuse immediately.
func (d *decoder) addSymbolValue(dst *ast.Builder, value []byte) (schema.ValueID, error) {
	if d.limits.MaxValues > 0 && len(dst.Document().ValueKinds) >= d.limits.MaxValues {
		return 0, d.fail(CodeLimit, "policy exceeds MaxValues")
	}
	if d.limits.MaxSymbolBytes > 0 && len(dst.Document().SymbolBytes)+len(value) > d.limits.MaxSymbolBytes {
		return 0, d.fail(CodeLimit, "symbol bytes exceed MaxSymbolBytes")
	}
	id, err := dst.AddSymbolValue(value)
	if err != nil {
		return 0, d.builderError(err)
	}
	return id, nil
}

// builderError maps an ast builder rejection onto a stable decode code.
func (d *decoder) builderError(err error) error {
	if errors.Is(err, ast.ErrSourceTooLarge) || errors.Is(err, ast.ErrSymbolTooLarge) ||
		errors.Is(err, ast.ErrTooManyValues) || errors.Is(err, ast.ErrTooManyEvidenceKinds) ||
		errors.Is(err, ast.ErrTooManyEvidenceStates) || errors.Is(err, ast.ErrTooManyOutcomes) ||
		errors.Is(err, ast.ErrTooManyNodes) || errors.Is(err, ast.ErrTooManyChildren) ||
		errors.Is(err, ast.ErrTooManyRemediations) || errors.Is(err, ast.ErrTooManyClauses) ||
		errors.Is(err, ast.ErrTooManySemanticEdges) || errors.Is(err, ast.ErrTooManyRequirements) ||
		errors.Is(err, ast.ErrTemplateTooLarge) || errors.Is(err, ast.ErrTooManyTemplates) ||
		errors.Is(err, ast.ErrTooManyAssumptions) || errors.Is(err, ast.ErrTooManyExplanations) ||
		errors.Is(err, ast.ErrTooManyUncertainty) {
		return d.fail(CodeLimit, err.Error())
	}
	return d.fail(CodeMalformed, err.Error())
}
