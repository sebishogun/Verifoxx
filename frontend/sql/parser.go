package sql

import (
	"bytes"
	"errors"
	"math"
	"unicode/utf8"

	public "github.com/sebishogun/nornrune/frontend"
)

type operandKind uint8

const (
	operandInvalid operandKind = iota
	operandNode
	operandField
	operandLiteral
	operandNull
)

type expressionOperand struct {
	literal public.Literal
	span    public.Span
	node    public.NodeID
	field   public.FieldID
	kind    operandKind
}

type expressionParser struct {
	tokens          *Tokens
	schema          *Schema
	builder         *public.Builder
	diagnostics     []Diagnostic
	literalBytes    []byte
	identifierBytes []byte
	nodeSpans       []public.Span
	list            []public.Literal
	limits          public.Limits
	position        int
	nesting         uint32
}

func (parser *expressionParser) parseOr() public.NodeID {
	left := parser.parseAnd()
	for !parser.failed() && parser.current() == TokenOr {
		parser.position++
		right := parser.parseAnd()
		left = parser.addGroup(public.NodeKindAny, left, right)
	}
	return left
}

func (parser *expressionParser) parseAnd() public.NodeID {
	left := parser.parseNot()
	for !parser.failed() && parser.current() == TokenAnd {
		parser.position++
		right := parser.parseNot()
		left = parser.addGroup(public.NodeKindAll, left, right)
	}
	return left
}

func (parser *expressionParser) parseNot() public.NodeID {
	if parser.current() != TokenNot {
		return parser.parseComparison()
	}
	start := parser.currentSpan().Start
	parser.position++
	if !parser.enter() {
		return 0
	}
	child := parser.parseNot()
	parser.leave()
	if child == 0 || parser.failed() {
		return 0
	}
	span := public.Span{Start: start, End: parser.nodeSpan(child).End}
	node, err := parser.builder.AddNot(child, span)
	return parser.completedNode(node, err, span)
}

func (parser *expressionParser) parseComparison() public.NodeID {
	left := parser.parsePrimary()
	if parser.failed() {
		return 0
	}
	if parser.current() == TokenNot && parser.peek(1) == TokenIn {
		parser.fail(public.CodeUnsupported, parser.currentSpan())
		return 0
	}
	if parser.current() == TokenIs {
		return parser.parseIs(left)
	}
	if parser.current() == TokenIn {
		return parser.parseIn(left)
	}
	operation := tokenCompareOperation(parser.current())
	if !operation.Valid() {
		return parser.ensureNode(left)
	}
	parser.position++
	right := parser.parsePrimary()
	if parser.failed() {
		return 0
	}
	span := mergeSQLSpan(left.span, right.span)
	if right.kind == operandNull {
		parser.fail(public.CodeUnsupported, right.span)
		return 0
	}
	if left.kind == operandNull {
		parser.fail(public.CodeUnsupported, left.span)
		return 0
	}
	if left.kind == operandField && right.kind == operandField {
		parser.fail(public.CodeUnsupported, span)
		return 0
	}
	if left.kind == operandField && right.kind == operandLiteral {
		node, err := parser.builder.AddCompare(left.field, operation, right.literal, span)
		return parser.completedNode(node, err, span)
	}
	if right.kind == operandField && left.kind == operandLiteral {
		node, err := parser.builder.AddCompare(right.field, reverseSQLCompare(operation), left.literal, span)
		return parser.completedNode(node, err, span)
	}
	parser.fail(public.CodeUnsupported, span)
	return 0
}

func (parser *expressionParser) parseIs(left expressionOperand) public.NodeID {
	if left.kind != operandField {
		parser.fail(public.CodeUnsupported, left.span)
		return 0
	}
	start := left.span.Start
	parser.position++
	negated := false
	if parser.current() == TokenNot {
		negated = true
		parser.position++
	}
	if parser.current() != TokenNull {
		parser.fail(public.CodeSyntax, parser.currentSpan())
		return 0
	}
	end := parser.currentSpan().End
	parser.position++
	span := public.Span{Start: start, End: end}
	node, err := parser.builder.AddDefined(left.field, span)
	node = parser.completedNode(node, err, span)
	if node == 0 || negated {
		return node
	}
	notNode, err := parser.builder.AddNot(node, span)
	return parser.completedNode(notNode, err, span)
}

func (parser *expressionParser) parseIn(left expressionOperand) public.NodeID {
	if left.kind != operandField {
		parser.fail(public.CodeUnsupported, left.span)
		return 0
	}
	parser.position++
	if parser.current() != TokenLParen {
		parser.fail(public.CodeSyntax, parser.currentSpan())
		return 0
	}
	parser.position++
	parser.list = parser.list[:0]
	if parser.current() == TokenRParen {
		parser.fail(public.CodeSyntax, parser.currentSpan())
		return 0
	}
	for {
		value := parser.parsePrimary()
		if parser.failed() {
			return 0
		}
		if value.kind != operandLiteral {
			parser.fail(public.CodeUnsupported, value.span)
			return 0
		}
		parser.list = append(parser.list, value.literal)
		if parser.current() != TokenComma {
			break
		}
		parser.position++
	}
	if parser.current() != TokenRParen {
		parser.fail(public.CodeSyntax, parser.currentSpan())
		return 0
	}
	end := parser.currentSpan().End
	parser.position++
	span := public.Span{Start: left.span.Start, End: end}
	node, err := parser.builder.AddIn(left.field, parser.list, span)
	return parser.completedNode(node, err, span)
}

func (parser *expressionParser) parsePrimary() expressionOperand {
	if parser.failed() {
		return expressionOperand{}
	}
	row := parser.position
	kind := parser.current()
	tokenSpan := parser.currentSpan()
	switch kind {
	case TokenLParen:
		parser.position++
		if !parser.enter() {
			return expressionOperand{}
		}
		node := parser.parseOr()
		parser.leave()
		if parser.current() != TokenRParen {
			parser.fail(public.CodeSyntax, parser.currentSpan())
			return expressionOperand{}
		}
		end := parser.currentSpan().End
		parser.position++
		groupSpan := public.Span{Start: tokenSpan.Start, End: end}
		parser.setNodeSpan(node, groupSpan)
		return expressionOperand{kind: operandNode, node: node, span: groupSpan}
	case TokenTrue, TokenFalse:
		parser.position++
		node, err := parser.builder.AddBoolean(kind == TokenTrue, tokenSpan)
		node = parser.completedNode(node, err, tokenSpan)
		return expressionOperand{kind: operandNode, node: node, span: tokenSpan}
	case TokenNull:
		parser.position++
		return expressionOperand{kind: operandNull, span: tokenSpan}
	case TokenInteger:
		parser.position++
		return expressionOperand{kind: operandLiteral, literal: public.IntegerLiteral(parser.tokens.Integers[row]), span: tokenSpan}
	case TokenMinus:
		parser.position++
		if parser.current() != TokenInteger {
			parser.fail(public.CodeSyntax, parser.currentSpan())
			return expressionOperand{}
		}
		value := parser.tokens.Integers[parser.position]
		end := parser.currentSpan().End
		parser.position++
		if value != math.MinInt64 {
			value = -value
		}
		return expressionOperand{kind: operandLiteral, literal: public.IntegerLiteral(value), span: public.Span{Start: tokenSpan.Start, End: end}}
	case TokenString:
		parser.position++
		start := len(parser.literalBytes)
		parser.literalBytes = appendDecodedQuote(parser.literalBytes, parser.tokenBytes(row))
		return expressionOperand{kind: operandLiteral, literal: public.StringLiteral(parser.literalBytes[start:]), span: tokenSpan}
	case TokenParameter:
		parser.position++
		literal, ok := parser.parameter(parser.tokenBytes(row))
		if !ok {
			parser.fail(public.CodeInvalidBinding, tokenSpan)
			return expressionOperand{}
		}
		return expressionOperand{kind: operandLiteral, literal: literal, span: tokenSpan}
	case TokenIdentifier:
		if parser.peek(1) == TokenLParen {
			parser.fail(public.CodeUnsupported, tokenSpan)
			return expressionOperand{}
		}
		parser.position++
		field, ok := parser.field(parser.tokenBytes(row))
		if !ok {
			parser.fail(public.CodeUnknownField, tokenSpan)
			return expressionOperand{}
		}
		return expressionOperand{kind: operandField, field: field, span: tokenSpan}
	default:
		parser.fail(public.CodeSyntax, tokenSpan)
		return expressionOperand{}
	}
}

func (parser *expressionParser) ensureNode(value expressionOperand) public.NodeID {
	if value.kind == operandNode {
		return value.node
	}
	if value.kind == operandField {
		binding := &parser.schema.Bindings.Fields[value.field-1]
		if binding.Kind != public.ValueKindBoolean {
			parser.fail(public.CodeType, value.span)
			return 0
		}
		node, err := parser.builder.AddCompare(value.field, public.CompareOpEqual, public.BooleanLiteral(true), value.span)
		return parser.completedNode(node, err, value.span)
	}
	parser.fail(public.CodeType, value.span)
	return 0
}

func (parser *expressionParser) addGroup(kind public.NodeKind, left, right public.NodeID) public.NodeID {
	if left == 0 || right == 0 || parser.failed() {
		return 0
	}
	children := [2]public.NodeID{left, right}
	span := mergeSQLSpan(parser.nodeSpan(left), parser.nodeSpan(right))
	var (
		node public.NodeID
		err  error
	)
	if kind == public.NodeKindAll {
		node, err = parser.builder.AddAll(children[:], span)
	} else {
		node, err = parser.builder.AddAny(children[:], span)
	}
	return parser.completedNode(node, err, span)
}

func (parser *expressionParser) completedNode(node public.NodeID, err error, sourceSpan public.Span) public.NodeID {
	if err == nil {
		parser.setNodeSpan(node, sourceSpan)
		return node
	}
	parser.fail(expressionBuilderCode(err), sourceSpan)
	return 0
}

func (parser *expressionParser) field(token []byte) (public.FieldID, bool) {
	name := token
	quoted := len(token) >= 2 && (token[0] == '"' || token[0] == '`')
	if quoted {
		parser.identifierBytes = parser.identifierBytes[:0]
		parser.identifierBytes = appendDecodedQuote(parser.identifierBytes, token)
		name = parser.identifierBytes
	}
	for row := range parser.schema.Bindings.Fields {
		candidate := parser.schema.Bindings.Fields[row].Source
		if quoted {
			if bytes.Equal(name, []byte(candidate)) {
				return public.FieldID(row + 1), true
			}
			continue
		}
		if foldedIdentifierEqual(name, candidate, parser.schema.Dialect) {
			return public.FieldID(row + 1), true
		}
	}
	return 0, false
}

func (parser *expressionParser) parameter(token []byte) (public.Literal, bool) {
	for row := range parser.schema.Parameters {
		parameter := &parser.schema.Parameters[row]
		if len(token) == len(parameter.Name) && bytes.Equal(token, []byte(parameter.Name)) {
			return parameter.Value, true
		}
	}
	return public.Literal{}, false
}

func (parser *expressionParser) tokenBytes(row int) []byte {
	return parser.tokens.Source[parser.tokens.Starts[row]:parser.tokens.Ends[row]]
}

func (parser *expressionParser) current() TokenKind {
	if parser.position < 0 || parser.position >= len(parser.tokens.Kinds) {
		return TokenEOF
	}
	return parser.tokens.Kinds[parser.position]
}

func (parser *expressionParser) peek(offset int) TokenKind {
	row := parser.position + offset
	if row < 0 || row >= len(parser.tokens.Kinds) {
		return TokenEOF
	}
	return parser.tokens.Kinds[row]
}

func (parser *expressionParser) currentSpan() public.Span {
	if parser.position < 0 || parser.position >= len(parser.tokens.Kinds) {
		end := uint32(len(parser.tokens.Source))
		return public.Span{Start: end, End: end}
	}
	return public.Span{Start: parser.tokens.Starts[parser.position], End: parser.tokens.Ends[parser.position]}
}

func (parser *expressionParser) nodeSpan(node public.NodeID) public.Span {
	if node == 0 || int(node) > len(parser.nodeSpans) {
		return public.Span{}
	}
	return parser.nodeSpans[node-1]
}

func (parser *expressionParser) setNodeSpan(node public.NodeID, sourceSpan public.Span) {
	if node == 0 {
		return
	}
	for len(parser.nodeSpans) < int(node) {
		parser.nodeSpans = append(parser.nodeSpans, public.Span{})
	}
	parser.nodeSpans[node-1] = sourceSpan
}

func (parser *expressionParser) enter() bool {
	parser.nesting++
	if parser.nesting > parser.limits.MaxDepth {
		parser.fail(public.CodeLimit, parser.currentSpan())
		return false
	}
	return true
}

func (parser *expressionParser) leave() {
	if parser.nesting != 0 {
		parser.nesting--
	}
}

func (parser *expressionParser) fail(code public.DiagnosticCode, sourceSpan public.Span) {
	if parser.failed() {
		return
	}
	parser.diagnostics = append(parser.diagnostics, Diagnostic{Span: sourceSpan, Dialect: parser.schema.Dialect, Code: code})
}

func (parser *expressionParser) failed() bool { return len(parser.diagnostics) != 0 }

func tokenCompareOperation(kind TokenKind) public.CompareOp {
	switch kind {
	case TokenEqual:
		return public.CompareOpEqual
	case TokenNotEqual:
		return public.CompareOpNotEqual
	case TokenLess:
		return public.CompareOpLess
	case TokenLessEqual:
		return public.CompareOpLessEqual
	case TokenGreater:
		return public.CompareOpGreater
	case TokenGreaterEqual:
		return public.CompareOpGreaterEqual
	default:
		return public.CompareOpInvalid
	}
}

func reverseSQLCompare(operation public.CompareOp) public.CompareOp {
	switch operation {
	case public.CompareOpLess:
		return public.CompareOpGreater
	case public.CompareOpLessEqual:
		return public.CompareOpGreaterEqual
	case public.CompareOpGreater:
		return public.CompareOpLess
	case public.CompareOpGreaterEqual:
		return public.CompareOpLessEqual
	default:
		return operation
	}
}

func expressionBuilderCode(err error) public.DiagnosticCode {
	switch {
	case err == nil:
		return public.CodeInvalid
	case errors.Is(err, public.ErrLimitExceeded):
		return public.CodeLimit
	case errors.Is(err, public.ErrInvalidBinding):
		return public.CodeInvalidBinding
	case errors.Is(err, public.ErrInvalidLiteral), errors.Is(err, public.ErrInvalidOperation):
		return public.CodeType
	default:
		return public.CodeInvalidPolicy
	}
}

func appendDecodedQuote(dst, token []byte) []byte {
	if len(token) < 2 {
		return dst
	}
	quote := token[0]
	for row := 1; row < len(token)-1; row++ {
		if token[row] == quote && row+1 < len(token)-1 && token[row+1] == quote {
			row++
		}
		dst = append(dst, token[row])
	}
	return dst
}

func foldedIdentifierEqual(token []byte, binding string, dialect Dialect) bool {
	if len(token) != len(binding) || !utf8.Valid(token) {
		return false
	}
	for row, current := range token {
		if current < utf8.RuneSelf {
			switch dialect {
			case DialectSnowflake:
				if current >= 'a' && current <= 'z' {
					current -= 'a' - 'A'
				}
			default:
				if current >= 'A' && current <= 'Z' {
					current += 'a' - 'A'
				}
			}
		}
		if current != binding[row] {
			return false
		}
	}
	return true
}

func mergeSQLSpan(left, right public.Span) public.Span {
	if left.Start == left.End {
		return right
	}
	if right.Start == right.End {
		return left
	}
	if right.Start < left.Start {
		left.Start = right.Start
	}
	if right.End > left.End {
		left.End = right.End
	}
	return left
}
