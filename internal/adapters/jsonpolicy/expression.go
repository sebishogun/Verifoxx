package jsonpolicy

import (
	"bytes"
	"math"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// Expression objects decode with arbitrary key order but no duplicate or
// unknown keys. Every scalar operand token is validated once during the
// object walk and decoded once more after the closing brace, when the field
// kind is known; the main scanner position is preserved across the re-read.
// Children and In values accumulate in reusable flat scratch with strict
// base/truncate discipline, so no per-expression slice is ever allocated.

var (
	keyOp              = []byte("op")
	keyArgs            = []byte("args")
	keyArg             = []byte("arg")
	keyField           = []byte("field")
	keyValue           = []byte("value")
	keyValues          = []byte("values")
	keyKind            = []byte("kind")
	keyState           = []byte("state")
	keySubject         = []byte("subject")
	keyScope           = []byte("scope")
	keyTiming          = []byte("timing")
	keyAll             = []byte("all")
	keyAny             = []byte("any")
	keyNot             = []byte("not")
	keyEqual           = []byte("equal")
	keyNotEqual        = []byte("not_equal")
	keyLess            = []byte("less")
	keyLessEqual       = []byte("less_equal")
	keyGreater         = []byte("greater")
	keyGreaterEqual    = []byte("greater_equal")
	keyIn              = []byte("in")
	keyExists          = []byte("exists")
	keyEvidenceMatches = []byte("evidence_matches")
)

// Expression key indexes; the bitmask of an expression object's keys is
// checked against the mask allowed by its op.
const (
	exprKeyOp = iota
	exprKeyArgs
	exprKeyArg
	exprKeyField
	exprKeyValue
	exprKeyValues
	exprKeyKind
	exprKeyState
	exprKeySubject
	exprKeyScope
	exprKeyTiming
	exprKeyExplanation
	exprKeyCount
)

const (
	exprBitOp          = uint16(1) << exprKeyOp
	exprBitArgs        = uint16(1) << exprKeyArgs
	exprBitArg         = uint16(1) << exprKeyArg
	exprBitField       = uint16(1) << exprKeyField
	exprBitValue       = uint16(1) << exprKeyValue
	exprBitValues      = uint16(1) << exprKeyValues
	exprBitKind        = uint16(1) << exprKeyKind
	exprBitState       = uint16(1) << exprKeyState
	exprBitSubject     = uint16(1) << exprKeySubject
	exprBitScope       = uint16(1) << exprKeyScope
	exprBitTiming      = uint16(1) << exprKeyTiming
	exprBitExplanation = uint16(1) << exprKeyExplanation
)

// exprKeyIndex maps a decoded expression-object key to its bit index, or -1
// for an unknown key.
func exprKeyIndex(key []byte) int {
	switch {
	case bytes.Equal(key, keyOp):
		return exprKeyOp
	case bytes.Equal(key, keyArgs):
		return exprKeyArgs
	case bytes.Equal(key, keyArg):
		return exprKeyArg
	case bytes.Equal(key, keyField):
		return exprKeyField
	case bytes.Equal(key, keyValue):
		return exprKeyValue
	case bytes.Equal(key, keyValues):
		return exprKeyValues
	case bytes.Equal(key, keyKind):
		return exprKeyKind
	case bytes.Equal(key, keyState):
		return exprKeyState
	case bytes.Equal(key, keySubject):
		return exprKeySubject
	case bytes.Equal(key, keyScope):
		return exprKeyScope
	case bytes.Equal(key, keyTiming):
		return exprKeyTiming
	case bytes.Equal(key, keyExplanation):
		return exprKeyExplanation
	}
	return -1
}

// exprOp is the bounded operation of one expression object.
type exprOp uint8

const (
	exprOpAll exprOp = iota
	exprOpAny
	exprOpNot
	exprOpEqual
	exprOpNotEqual
	exprOpLess
	exprOpLessEqual
	exprOpGreater
	exprOpGreaterEqual
	exprOpIn
	exprOpExists
	exprOpEvidenceMatches
)

// parseExprOp maps the decoded op string to its bounded operation.
func parseExprOp(b []byte) (exprOp, bool) {
	switch {
	case bytes.Equal(b, keyAll):
		return exprOpAll, true
	case bytes.Equal(b, keyAny):
		return exprOpAny, true
	case bytes.Equal(b, keyNot):
		return exprOpNot, true
	case bytes.Equal(b, keyEqual):
		return exprOpEqual, true
	case bytes.Equal(b, keyNotEqual):
		return exprOpNotEqual, true
	case bytes.Equal(b, keyLess):
		return exprOpLess, true
	case bytes.Equal(b, keyLessEqual):
		return exprOpLessEqual, true
	case bytes.Equal(b, keyGreater):
		return exprOpGreater, true
	case bytes.Equal(b, keyGreaterEqual):
		return exprOpGreaterEqual, true
	case bytes.Equal(b, keyIn):
		return exprOpIn, true
	case bytes.Equal(b, keyExists):
		return exprOpExists, true
	case bytes.Equal(b, keyEvidenceMatches):
		return exprOpEvidenceMatches, true
	}
	return 0, false
}

// exprKeyMask returns the exact key set each op accepts.
func exprKeyMask(op exprOp) uint16 {
	switch op {
	case exprOpAll, exprOpAny:
		return exprBitOp | exprBitArgs
	case exprOpNot:
		return exprBitOp | exprBitArg
	case exprOpIn:
		return exprBitOp | exprBitField | exprBitValues
	case exprOpExists:
		return exprBitOp | exprBitField
	case exprOpEvidenceMatches:
		return exprBitOp | exprBitKind | exprBitState | exprBitSubject | exprBitScope | exprBitTiming | exprBitExplanation
	}
	return exprBitOp | exprBitField | exprBitValue
}

func exprRequiredMask(op exprOp) uint16 {
	if op == exprOpEvidenceMatches {
		return exprBitOp | exprBitKind | exprBitState | exprBitExplanation
	}
	return exprKeyMask(op)
}

// compareOpFor maps a comparison exprOp to its AST operation.
func compareOpFor(op exprOp) ast.CompareOp {
	switch op {
	case exprOpEqual:
		return ast.CompareOpEqual
	case exprOpNotEqual:
		return ast.CompareOpNotEqual
	case exprOpLess:
		return ast.CompareOpLess
	case exprOpLessEqual:
		return ast.CompareOpLessEqual
	case exprOpGreater:
		return ast.CompareOpGreater
	case exprOpGreaterEqual:
		return ast.CompareOpGreaterEqual
	}
	return ast.CompareOpInvalid
}

func (d *decoder) checkNodeLimit(dst *ast.Builder) error {
	if d.limits.MaxNodes > 0 && dst.Len() >= d.limits.MaxNodes {
		return d.fail(CodeLimit, "expression exceeds MaxNodes")
	}
	return nil
}

// decodeExpression consumes one expression object at d.pos and appends its
// node to dst, returning the root NodeID. The object source span is exactly
// [opening '{', position after the closing '}'). depth starts at 1 for the
// root expression and increments per nesting level.
func (d *decoder) decodeExpression(dst *ast.Builder, depth int) (schema.NodeID, error) {
	if d.limits.MaxDepth > 0 && depth > d.limits.MaxDepth {
		return 0, d.fail(CodeLimit, "expression exceeds MaxDepth")
	}
	if err := d.checkNodeLimit(dst); err != nil {
		return 0, err
	}
	objectStart := d.pos
	if d.atEOF() {
		return 0, d.fail(CodeTruncated, "unexpected end of input in expression object")
	}
	if d.src[d.pos] != '{' {
		return 0, d.fail(CodeInvalidType, "expected an expression object")
	}
	d.pos++

	var op exprOp
	var opStart int
	var seen uint16
	var fieldID schema.FieldID
	var valueToken ast.SourceSpan
	var valuesToken ast.SourceSpan
	var kindToken ast.SourceSpan
	var stateToken ast.SourceSpan
	var subjectToken ast.SourceSpan
	var scopeToken ast.SourceSpan
	var timingToken ast.SourceSpan
	var issueTemplates [ast.EvidenceIssueReasonCount]schema.TemplateID
	var argsBase int
	var notChild schema.NodeID

	requireKey := false
	for {
		d.skipWS()
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input in expression object")
		}
		if d.src[d.pos] == '}' {
			if requireKey {
				return 0, d.fail(CodeMalformed, "trailing comma in expression object")
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
		index := exprKeyIndex(key)
		if index < 0 {
			return 0, d.failAt(CodeUnknownKey, keyStart, "unknown key in expression object")
		}
		bit := uint16(1) << uint(index)
		if seen&bit != 0 {
			return 0, d.failAt(CodeDuplicateKey, keyStart, "duplicate key in expression object")
		}
		seen |= bit
		d.skipWS()
		if err := d.expectPunct(':'); err != nil {
			return 0, err
		}
		d.skipWS()
		switch index {
		case exprKeyOp:
			opStart = d.pos
			v, err := d.expectString(&d.valueScratch)
			if err != nil {
				return 0, err
			}
			var ok bool
			op, ok = parseExprOp(v)
			if !ok {
				return 0, d.failAt(CodeMalformed, opStart, "unknown op")
			}
		case exprKeyArgs:
			argsBase = len(d.nodeScratch)
			if err := d.decodeArgsArray(dst, depth); err != nil {
				return 0, err
			}
		case exprKeyArg:
			child, err := d.decodeExpression(dst, depth+1)
			if err != nil {
				return 0, err
			}
			notChild = child
		case exprKeyField:
			fieldStart := d.pos
			fieldBytes, err := d.expectString(&d.valueScratch)
			if err != nil {
				return 0, err
			}
			symbolID, ok := d.symbols.Lookup(fieldBytes)
			if !ok {
				return 0, d.failAt(CodeInvalidReference, fieldStart, "unknown field")
			}
			fid, ok := d.fields.Lookup(symbolID)
			if !ok {
				return 0, d.failAt(CodeInvalidReference, fieldStart, "unknown field")
			}
			fieldID = fid
		case exprKeyValue:
			tokenStart := d.pos
			if err := d.skipScalar(); err != nil {
				return 0, err
			}
			valueToken = ast.SourceSpan{Start: uint32(tokenStart), End: uint32(d.pos)}
		case exprKeyValues:
			tokenStart := d.pos
			if err := d.skipValuesArray(); err != nil {
				return 0, err
			}
			valuesToken = ast.SourceSpan{Start: uint32(tokenStart), End: uint32(d.pos)}
		case exprKeyKind:
			tokenStart := d.pos
			if _, err := d.expectString(&d.valueScratch); err != nil {
				return 0, err
			}
			kindToken = ast.SourceSpan{Start: uint32(tokenStart), End: uint32(d.pos)}
		case exprKeyState:
			tokenStart := d.pos
			if _, err := d.expectString(&d.valueScratch); err != nil {
				return 0, err
			}
			stateToken = ast.SourceSpan{Start: uint32(tokenStart), End: uint32(d.pos)}
		case exprKeySubject, exprKeyScope, exprKeyTiming:
			tokenStart := d.pos
			if _, err := d.expectString(&d.valueScratch); err != nil {
				return 0, err
			}
			token := ast.SourceSpan{Start: uint32(tokenStart), End: uint32(d.pos)}
			switch index {
			case exprKeySubject:
				subjectToken = token
			case exprKeyScope:
				scopeToken = token
			case exprKeyTiming:
				timingToken = token
			}
		case exprKeyExplanation:
			issueTemplates, err = d.decodeEvidenceExplanation(dst)
			if err != nil {
				return 0, err
			}
		}
		d.skipWS()
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input in expression object")
		}
		if d.src[d.pos] == ',' {
			d.pos++
			requireKey = true
			continue
		}
		if d.src[d.pos] != '}' {
			return 0, d.fail(CodeMalformed, "expected ',' or '}'")
		}
	}

	if seen&exprBitOp == 0 {
		return 0, d.failAt(CodeMissingKey, objectStart, "expression object missing \"op\"")
	}
	mask := exprKeyMask(op)
	if seen&^mask != 0 {
		return 0, d.failAt(CodeInvalidArity, objectStart, "key not allowed for op")
	}
	if exprRequiredMask(op)&^seen != 0 {
		return 0, d.failAt(CodeInvalidArity, objectStart, "missing required key for op")
	}
	span := ast.SourceSpan{Start: uint32(objectStart), End: uint32(d.pos)}

	switch op {
	case exprOpAll, exprOpAny:
		kind := ast.NodeKindAll
		if op == exprOpAny {
			kind = ast.NodeKindAny
		}
		if err := d.checkNodeLimit(dst); err != nil {
			d.nodeScratch = d.nodeScratch[:argsBase]
			return 0, err
		}
		node, err := dst.AddGroup(kind, d.nodeScratch[argsBase:], span)
		if err != nil {
			return 0, d.builderError(err)
		}
		d.nodeScratch = d.nodeScratch[:argsBase]
		return node, nil
	case exprOpNot:
		if err := d.checkNodeLimit(dst); err != nil {
			return 0, err
		}
		node, err := dst.AddNot(notChild, span)
		if err != nil {
			return 0, d.builderError(err)
		}
		return node, nil
	case exprOpIn:
		fieldKind, ok := d.fields.Kind(fieldID)
		if !ok {
			return 0, d.failAt(CodeInvalidReference, int(valuesToken.Start), "field missing from schema")
		}
		if fieldKind == schema.ValueKindPresence {
			return 0, d.failAt(CodeInvalidType, int(valuesToken.Start), "presence fields cannot take literal values")
		}
		if err := d.checkNodeLimit(dst); err != nil {
			return 0, err
		}
		base := len(d.valueIDScratch)
		if err := d.decodeValuesArray(dst, int(valuesToken.Start), int(valuesToken.End), fieldKind); err != nil {
			return 0, err
		}
		node, err := dst.AddIn(fieldID, d.valueIDScratch[base:], span)
		if err != nil {
			return 0, d.builderError(err)
		}
		d.valueIDScratch = d.valueIDScratch[:base]
		return node, nil
	case exprOpExists:
		if err := d.checkNodeLimit(dst); err != nil {
			return 0, err
		}
		node, err := dst.AddExists(fieldID, span)
		if err != nil {
			return 0, d.builderError(err)
		}
		return node, nil
	case exprOpEvidenceMatches:
		kindID, err := d.resolveKindToken(dst, kindToken)
		if err != nil {
			return 0, err
		}
		stateID, err := d.resolveStateToken(dst, stateToken)
		if err != nil {
			return 0, err
		}
		if err := d.checkNodeLimit(dst); err != nil {
			return 0, err
		}
		subject, err := d.decodeOptionalSymbolToken(dst, subjectToken)
		if err != nil {
			return 0, err
		}
		scope, err := d.decodeOptionalSymbolToken(dst, scopeToken)
		if err != nil {
			return 0, err
		}
		timing, err := d.decodeOptionalSymbolToken(dst, timingToken)
		if err != nil {
			return 0, err
		}
		node, err := dst.AddEvidenceMatch(kindID, stateID, subject, scope, timing, span)
		if err != nil {
			return 0, d.builderError(err)
		}
		if err := dst.SetEvidenceIssueTemplates(node, issueTemplates); err != nil {
			return 0, d.builderError(err)
		}
		return node, nil
	default:
		fieldKind, ok := d.fields.Kind(fieldID)
		if !ok {
			return 0, d.failAt(CodeInvalidReference, int(valueToken.Start), "field missing from schema")
		}
		if err := d.checkNodeLimit(dst); err != nil {
			return 0, err
		}
		valueID, err := d.decodeValueToken(dst, int(valueToken.Start), int(valueToken.End), fieldKind)
		if err != nil {
			return 0, err
		}
		node, err := dst.AddCompare(fieldID, compareOpFor(op), valueID, span)
		if err != nil {
			return 0, d.builderError(err)
		}
		return node, nil
	}
}

func (d *decoder) decodeOptionalSymbolToken(dst *ast.Builder, token ast.SourceSpan) (schema.ValueID, error) {
	if token.End == 0 {
		return 0, nil
	}
	return d.decodeValueToken(dst, int(token.Start), int(token.End), schema.ValueKindSymbol)
}

// decodeArgsArray consumes the args array, appending each decoded child node
// to nodeScratch. The caller copies nodeScratch[base:] into the group and
// truncates back to base; nested children restore their own ranges first.
func (d *decoder) decodeArgsArray(dst *ast.Builder, depth int) error {
	if err := d.expectArrayStart(); err != nil {
		return err
	}
	d.skipWS()
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input in args array")
	}
	if d.src[d.pos] == ']' {
		d.pos++
		return d.fail(CodeInvalidArity, "empty args array")
	}
	count := 0
	for {
		if d.limits.MaxArrayItems > 0 && count >= d.limits.MaxArrayItems {
			return d.fail(CodeLimit, "expression array exceeds MaxArrayItems")
		}
		child, err := d.decodeExpression(dst, depth+1)
		if err != nil {
			return err
		}
		d.nodeScratch = append(d.nodeScratch, child)
		count++
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in args array")
		}
		switch d.src[d.pos] {
		case ',':
			d.pos++
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in args array")
			}
			if d.src[d.pos] == ']' {
				return d.fail(CodeMalformed, "trailing comma in args array")
			}
		case ']':
			d.pos++
			return nil
		default:
			return d.fail(CodeMalformed, "expected ',' or ']'")
		}
	}
}

// skipScalar validates and consumes one scalar literal token (string,
// integer, or boolean). The token is re-decoded once the field kind is known.
func (d *decoder) skipScalar() error {
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input")
	}
	switch b := d.src[d.pos]; {
	case b == '"':
		_, err := d.parseString(&d.valueScratch)
		return err
	case b == '-' || isDigit(b):
		_, err := d.parseInteger()
		return err
	case b == 't' || b == 'f':
		_, isNull, err := d.parseLiteral()
		if err != nil {
			return err
		}
		if isNull {
			return d.fail(CodeInvalidType, "expected a scalar literal")
		}
		return nil
	default:
		return d.fail(CodeInvalidType, "expected a scalar literal")
	}
}

// skipValuesArray validates the values array shape: every element must be a
// scalar literal. The whole array token is re-decoded under the field kind
// once it is known.
func (d *decoder) skipValuesArray() error {
	if err := d.expectArrayStart(); err != nil {
		return err
	}
	d.skipWS()
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input in values array")
	}
	if d.src[d.pos] == ']' {
		d.pos++
		return d.fail(CodeInvalidArity, "empty values array")
	}
	count := 0
	for {
		if d.limits.MaxArrayItems > 0 && count >= d.limits.MaxArrayItems {
			return d.fail(CodeLimit, "expression array exceeds MaxArrayItems")
		}
		if err := d.skipScalar(); err != nil {
			return err
		}
		count++
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in values array")
		}
		switch d.src[d.pos] {
		case ',':
			d.pos++
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in values array")
			}
			if d.src[d.pos] == ']' {
				return d.fail(CodeMalformed, "trailing comma in values array")
			}
		case ']':
			d.pos++
			return nil
		default:
			return d.fail(CodeMalformed, "expected ',' or ']'")
		}
	}
}

// decodeValueToken re-parses a recorded scalar token range as a literal of
// the field kind. The main scanner position is preserved.
func (d *decoder) decodeValueToken(dst *ast.Builder, start, end int, kind schema.ValueKind) (schema.ValueID, error) {
	if d.limits.MaxValues > 0 && len(dst.Document().ValueKinds) >= d.limits.MaxValues {
		return 0, d.fail(CodeLimit, "expression exceeds MaxValues")
	}
	saved := d.pos
	d.pos = start
	id, err := d.decodeLiteral(dst, kind)
	if err == nil && d.pos != end {
		err = d.fail(CodeMalformed, "value token misparsed")
	}
	d.pos = saved
	return id, err
}

// decodeValuesArray re-parses a recorded values array token range, decoding
// every element as a literal of the field kind into valueIDScratch. The main
// scanner position is preserved.
func (d *decoder) decodeValuesArray(dst *ast.Builder, start, end int, kind schema.ValueKind) error {
	saved := d.pos
	d.pos = start
	err := d.decodeValuesInPlace(dst, kind)
	if err == nil && d.pos != end {
		err = d.fail(CodeMalformed, "values token misparsed")
	}
	d.pos = saved
	return err
}

// decodeValuesInPlace consumes a values array at d.pos, appending each
// decoded literal to valueIDScratch.
func (d *decoder) decodeValuesInPlace(dst *ast.Builder, kind schema.ValueKind) error {
	if err := d.expectArrayStart(); err != nil {
		return err
	}
	d.skipWS()
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input in values array")
	}
	if d.src[d.pos] == ']' {
		d.pos++
		return nil
	}
	count := 0
	for {
		if d.limits.MaxArrayItems > 0 && count >= d.limits.MaxArrayItems {
			return d.fail(CodeLimit, "expression array exceeds MaxArrayItems")
		}
		if d.limits.MaxValues > 0 && len(dst.Document().ValueKinds) >= d.limits.MaxValues {
			return d.fail(CodeLimit, "expression exceeds MaxValues")
		}
		id, err := d.decodeLiteral(dst, kind)
		if err != nil {
			return err
		}
		d.valueIDScratch = append(d.valueIDScratch, id)
		count++
		d.skipWS()
		if d.atEOF() {
			return d.fail(CodeTruncated, "unexpected end of input in values array")
		}
		switch d.src[d.pos] {
		case ',':
			d.pos++
			d.skipWS()
			if d.atEOF() {
				return d.fail(CodeTruncated, "unexpected end of input in values array")
			}
			if d.src[d.pos] == ']' {
				return d.fail(CodeMalformed, "trailing comma in values array")
			}
		case ']':
			d.pos++
			return nil
		default:
			return d.fail(CodeMalformed, "expected ',' or ']'")
		}
	}
}

// decodeLiteral consumes one literal token at d.pos as the field kind.
func (d *decoder) decodeLiteral(dst *ast.Builder, kind schema.ValueKind) (schema.ValueID, error) {
	switch kind {
	case schema.ValueKindSymbol:
		v, err := d.expectString(&d.valueScratch)
		if err != nil {
			return 0, err
		}
		return d.addSymbolValue(dst, v)
	case schema.ValueKindInteger:
		v, err := d.expectInteger()
		if err != nil {
			return 0, err
		}
		return dst.AddIntegerValue(v)
	case schema.ValueKindBoolean:
		v, err := d.expectBool()
		if err != nil {
			return 0, err
		}
		return dst.AddBooleanValue(v)
	case schema.ValueKindTimestamp:
		v, err := d.expectString(&d.valueScratch)
		if err != nil {
			return 0, err
		}
		ns, err := d.parseTimestamp(v)
		if err != nil {
			return 0, err
		}
		return dst.AddTimestampValue(ns)
	default:
		return 0, d.fail(CodeInvalidType, "presence fields cannot take literal values")
	}
}

// resolveKindToken re-parses a recorded kind token range and resolves it
// against the evidence kind catalog. The main scanner position is preserved.
func (d *decoder) resolveKindToken(dst *ast.Builder, token ast.SourceSpan) (schema.EvidenceKindID, error) {
	saved := d.pos
	d.pos = int(token.Start)
	v, err := d.expectString(&d.valueScratch)
	var id schema.EvidenceKindID
	if err == nil {
		id, err = d.resolveEvidenceKind(dst, v, int(token.Start))
	}
	d.pos = saved
	return id, err
}

// resolveStateToken re-parses a recorded state token range and resolves it
// against the evidence state catalog. The main scanner position is preserved.
func (d *decoder) resolveStateToken(dst *ast.Builder, token ast.SourceSpan) (schema.EvidenceStateID, error) {
	saved := d.pos
	d.pos = int(token.Start)
	v, err := d.expectString(&d.valueScratch)
	var id schema.EvidenceStateID
	if err == nil {
		id, err = d.resolveEvidenceState(dst, v, int(token.Start))
	}
	d.pos = saved
	return id, err
}

// resolveEvidenceKind resolves a decoded kind name against the AST evidence
// kind catalog by byte equality.
func (d *decoder) resolveEvidenceKind(dst *ast.Builder, name []byte, offset int) (schema.EvidenceKindID, error) {
	doc := dst.Document()
	if row, ok := findCatalogName(doc, doc.EvidenceKindNames, name); ok {
		return schema.EvidenceKindID(row), nil
	}
	return 0, d.failAt(CodeInvalidReference, offset, "unknown evidence kind")
}

// resolveEvidenceState resolves a decoded state name against the AST
// evidence state catalog by byte equality.
func (d *decoder) resolveEvidenceState(dst *ast.Builder, name []byte, offset int) (schema.EvidenceStateID, error) {
	doc := dst.Document()
	if row, ok := findCatalogName(doc, doc.EvidenceStateNames, name); ok {
		return schema.EvidenceStateID(row), nil
	}
	return 0, d.failAt(CodeInvalidReference, offset, "unknown evidence state")
}

// parseFixedInt parses b[start:end] as a decimal integer.
func parseFixedInt(b []byte, start, end int) (int, bool) {
	v := 0
	for i := start; i < end; i++ {
		if !isDigit(b[i]) {
			return 0, false
		}
		v = v*10 + int(b[i]-'0')
	}
	return v, true
}

// parseTimestamp converts an RFC3339 timestamp byte string to Unix
// nanoseconds without allocating or converting to a Go string. Accepted
// grammar: YYYY-MM-DDTHH:MM:SS[.1-9 digits](Z|+HH:MM|-HH:MM). Dates, times,
// zones, and the int64 nanosecond range are validated.
func (d *decoder) parseTimestamp(b []byte) (int64, error) {
	if len(b) < 20 || b[4] != '-' || b[7] != '-' || b[10] != 'T' ||
		b[13] != ':' || b[16] != ':' {
		return 0, d.fail(CodeMalformed, "invalid RFC3339 timestamp")
	}
	year, ok := parseFixedInt(b, 0, 4)
	if !ok {
		return 0, d.fail(CodeMalformed, "invalid RFC3339 timestamp")
	}
	month, ok := parseFixedInt(b, 5, 7)
	if !ok {
		return 0, d.fail(CodeMalformed, "invalid RFC3339 timestamp")
	}
	day, ok := parseFixedInt(b, 8, 10)
	if !ok {
		return 0, d.fail(CodeMalformed, "invalid RFC3339 timestamp")
	}
	hour, ok := parseFixedInt(b, 11, 13)
	if !ok {
		return 0, d.fail(CodeMalformed, "invalid RFC3339 timestamp")
	}
	minute, ok := parseFixedInt(b, 14, 16)
	if !ok {
		return 0, d.fail(CodeMalformed, "invalid RFC3339 timestamp")
	}
	second, ok := parseFixedInt(b, 17, 19)
	if !ok {
		return 0, d.fail(CodeMalformed, "invalid RFC3339 timestamp")
	}

	i := 19
	frac := int64(0)
	fracDigits := 0
	if i < len(b) && b[i] == '.' {
		i++
		for i < len(b) && isDigit(b[i]) {
			if fracDigits >= 9 {
				return 0, d.fail(CodeMalformed, "timestamp fraction exceeds nanosecond precision")
			}
			frac = frac*10 + int64(b[i]-'0')
			fracDigits++
			i++
		}
		if fracDigits == 0 {
			return 0, d.fail(CodeMalformed, "timestamp fraction requires digits")
		}
	}
	for fracDigits < 9 {
		frac *= 10
		fracDigits++
	}

	var zoneSeconds int64
	if i >= len(b) {
		return 0, d.fail(CodeMalformed, "timestamp missing zone")
	}
	switch b[i] {
	case 'Z':
		i++
	case '+', '-':
		if i+6 > len(b) || b[i+3] != ':' {
			return 0, d.fail(CodeMalformed, "invalid timestamp zone")
		}
		zh, ok := parseFixedInt(b, i+1, i+3)
		if !ok || zh > 23 {
			return 0, d.fail(CodeMalformed, "invalid timestamp zone")
		}
		zm, ok := parseFixedInt(b, i+4, i+6)
		if !ok || zm > 59 {
			return 0, d.fail(CodeMalformed, "invalid timestamp zone")
		}
		zoneSeconds = int64(zh)*3600 + int64(zm)*60
		if b[i] == '-' {
			zoneSeconds = -zoneSeconds
		}
		i += 6
	default:
		return 0, d.fail(CodeMalformed, "invalid timestamp zone")
	}
	if i != len(b) {
		return 0, d.fail(CodeMalformed, "trailing data in timestamp")
	}

	if month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) {
		return 0, d.fail(CodeMalformed, "invalid timestamp date")
	}
	if hour > 23 || minute > 59 || second > 59 {
		return 0, d.fail(CodeMalformed, "invalid timestamp time")
	}

	days := daysFromCivil(int64(year), int64(month), int64(day))
	seconds := days*86400 + int64(hour)*3600 + int64(minute)*60 + int64(second) - zoneSeconds
	if seconds >= 0 {
		if uint64(seconds) > (uint64(math.MaxInt64)-uint64(frac))/1e9 {
			return 0, d.fail(CodeLimit, "timestamp exceeds int64 nanoseconds")
		}
		return seconds*1e9 + frac, nil
	}
	magnitude := uint64(-seconds)
	if magnitude > (uint64(math.MaxInt64)+1+uint64(frac))/1e9 {
		return 0, d.fail(CodeLimit, "timestamp exceeds int64 nanoseconds")
	}
	return -int64(magnitude*1e9) + frac, nil
}

// isLeap reports whether year is a leap year in the proleptic Gregorian
// calendar.
func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// daysInMonth returns the number of days in month of year, or 0 for an
// invalid month.
func daysInMonth(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(year) {
			return 29
		}
		return 28
	}
	return 0
}

// daysFromCivil returns the number of days from 1970-01-01 to the given
// proleptic Gregorian civil date (Howard Hinnant's algorithm).
func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	var era int64
	if y >= 0 {
		era = y / 400
	} else {
		era = (y - 399) / 400
	}
	yoe := y - era*400
	var mo int64
	if m > 2 {
		mo = m - 3
	} else {
		mo = m + 9
	}
	doy := (153*mo+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}
