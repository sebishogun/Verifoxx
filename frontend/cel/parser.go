// Package cel translates a bounded CEL subset into the shared semantic
// frontend representation.
package cel

import (
	"bytes"
	"strings"
	"unicode/utf8"

	celgo "cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	celtypes "cel.dev/cel-go/common/types"

	public "github.com/sebishogun/verifoxx/frontend"
)

// Parsed owns the checked CEL tree and the source metadata needed for exact
// byte-span translation. Its parser objects never leave this package.
type Parsed struct {
	ast       *celgo.Ast
	source    []byte
	bindings  public.BindingSet
	exprSpans []public.Span
	limits    public.Limits
	scratch   scratchSizes
}

type scratchSizes struct {
	children    int
	literals    int
	stringBytes int
}

type expressionFrame struct {
	expr    celast.Expr
	visited bool
}

// Parse checks source and declarations with bounded official CEL APIs.
func Parse(source []byte, bindings public.BindingSet, limits public.Limits) (*Parsed, []public.Diagnostic) {
	if diagnostics := validateInputs(source, bindings, limits); len(diagnostics) != 0 {
		return nil, diagnostics
	}
	runeToByte, lineRuneStarts := indexSource(source)
	environment, diagnostic := newEnvironment(bindings, limits)
	if diagnostic.Code.Valid() {
		return nil, []public.Diagnostic{diagnostic}
	}

	parsedAST, issues := environment.Parse(string(source))
	if issues != nil && issues.Err() != nil {
		return nil, issueDiagnostics(source, runeToByte, lineRuneStarts, issues, public.CodeSyntax, limits.MaxDiagnostics)
	}
	postorder, exprSpans, scratch := expressionSpans(source, runeToByte, parsedAST.NativeRep().Expr(), parsedAST.NativeRep().SourceInfo())
	if diagnostics := unsupportedDiagnostics(postorder, exprSpans, parsedAST.NativeRep().SourceInfo(), source, runeToByte, limits.MaxDiagnostics); len(diagnostics) != 0 {
		return nil, diagnostics
	}

	checkedAST, issues := environment.Check(parsedAST)
	if issues != nil && issues.Err() != nil {
		return nil, issueDiagnostics(source, runeToByte, lineRuneStarts, issues, public.CodeType, limits.MaxDiagnostics)
	}
	if checkedAST == nil || checkedAST.NativeRep() == nil {
		return nil, []public.Diagnostic{newDiagnostic(public.CodeInvalidPolicy, public.Span{}, 0, 0)}
	}
	if checkedAST.OutputType().Kind() != celgo.BoolKind && !boundBooleanExpression(checkedAST.NativeRep().Expr(), bindings) {
		span := exprSpan(exprSpans, checkedAST.NativeRep().Expr())
		return nil, []public.Diagnostic{newDiagnostic(public.CodeType, span, checkedAST.NativeRep().Expr().ID(), 0)}
	}

	return &Parsed{
		ast: checkedAST, source: bytes.Clone(source), bindings: cloneBindings(bindings), limits: limits,
		exprSpans: exprSpans, scratch: scratch,
	}, nil
}

func validateInputs(source []byte, bindings public.BindingSet, limits public.Limits) []public.Diagnostic {
	if !limits.Valid() {
		return []public.Diagnostic{newDiagnostic(public.CodeLimit, public.Span{}, 0, 0)}
	}
	if uint64(len(source)) > uint64(limits.MaxSourceBytes) {
		return []public.Diagnostic{newDiagnostic(public.CodeLimit, public.Span{End: uint32(min(len(source), 1))}, 0, 0)}
	}
	if !utf8.Valid(source) {
		start := firstInvalidUTF8(source)
		return []public.Diagnostic{newDiagnostic(public.CodeSyntax, public.Span{Start: uint32(start), End: uint32(start + 1)}, 0, 0)}
	}
	if uint64(len(bindings.Fields)) > uint64(limits.MaxFields) || bindingStringBytes(bindings) > uint64(limits.MaxStringBytes) {
		return []public.Diagnostic{newDiagnostic(public.CodeLimit, public.Span{}, 0, 0)}
	}
	if err := bindings.Validate(limits); err != nil {
		return []public.Diagnostic{newDiagnostic(public.CodeInvalidBinding, public.Span{}, 0, 0)}
	}
	return nil
}

func bindingStringBytes(bindings public.BindingSet) uint64 {
	total := uint64(len(bindings.Name)) + uint64(len(bindings.Version)) + uint64(len(bindings.Decision))
	for row := range bindings.Fields {
		total += uint64(len(bindings.Fields[row].Source)) + uint64(len(bindings.Fields[row].Target))
	}
	return total
}

func firstInvalidUTF8(source []byte) int {
	for offset := 0; offset < len(source); {
		_, size := utf8.DecodeRune(source[offset:])
		if size == 1 && source[offset] >= utf8.RuneSelf {
			return offset
		}
		offset += size
	}
	return 0
}

func newEnvironment(bindings public.BindingSet, limits public.Limits) (*celgo.Env, public.Diagnostic) {
	options := make([]celgo.EnvOption, 0, len(bindings.Fields)+6)
	options = append(options,
		celgo.ClearMacros(),
		celgo.HomogeneousAggregateLiterals(),
		celgo.ParserExpressionSizeLimit(int(limits.MaxSourceBytes)),
		celgo.ParserRecursionLimit(int(limits.MaxDepth)),
		celgo.ExpressionNodeLimit(int(limits.MaxNodes)),
		celgo.ExpressionNestingDepthLimit(int(limits.MaxDepth)),
	)
	type declaration struct {
		name   string
		scalar bool
	}
	declarations := make([]declaration, 0, len(bindings.Fields))
	for row := range bindings.Fields {
		binding := &bindings.Fields[row]
		root, selected := selectionRoot(binding.Source)
		if reservedCELIdentifier(root) {
			return nil, newDiagnostic(public.CodeInvalidBinding, public.Span{}, int64(row+1), public.FieldID(row+1))
		}
		found := -1
		for previous := range declarations {
			if declarations[previous].name == root {
				found = previous
				break
			}
		}
		if found >= 0 {
			if declarations[found].scalar || !selected {
				return nil, newDiagnostic(public.CodeInvalidBinding, public.Span{}, int64(row+1), public.FieldID(row+1))
			}
			continue
		}
		declarations = append(declarations, declaration{name: root, scalar: !selected})
		if selected {
			options = append(options, celgo.Variable(root, celgo.MapType(celgo.StringType, celgo.DynType)))
			continue
		}
		options = append(options, celgo.Variable(root, celType(binding.Kind)))
	}
	environment, err := celgo.NewEnv(options...)
	if err != nil {
		return nil, newDiagnostic(public.CodeInvalidBinding, public.Span{}, 0, 0)
	}
	return environment, public.Diagnostic{}
}

func reservedCELIdentifier(identifier string) bool {
	switch identifier {
	case "as", "break", "const", "continue", "else", "false", "for", "function", "if", "import", "in", "let", "loop", "package", "namespace", "null", "return", "true", "var", "void", "while":
		return true
	default:
		return false
	}
}

func selectionRoot(path string) (string, bool) {
	if dot := strings.IndexByte(path, '.'); dot >= 0 {
		return path[:dot], true
	}
	return path, false
}

func celType(kind public.ValueKind) *celgo.Type {
	switch kind {
	case public.ValueKindString:
		return celgo.StringType
	case public.ValueKindInteger:
		return celgo.IntType
	case public.ValueKindBoolean:
		return celgo.BoolType
	default:
		return celgo.DynType
	}
}

func indexSource(source []byte) ([]uint32, []uint32) {
	runeToByte := make([]uint32, 0, utf8.RuneCount(source)+1)
	lineRuneStarts := make([]uint32, 1, bytes.Count(source, []byte{'\n'})+1)
	runeIndex := uint32(0)
	for byteOffset, value := range string(source) {
		runeToByte = append(runeToByte, uint32(byteOffset))
		runeIndex++
		if value == '\n' {
			lineRuneStarts = append(lineRuneStarts, runeIndex)
		}
	}
	runeToByte = append(runeToByte, uint32(len(source)))
	return runeToByte, lineRuneStarts
}

func expressionSpans(source []byte, runeToByte []uint32, root celast.Expr, info *celast.SourceInfo) ([]celast.Expr, []public.Span, scratchSizes) {
	stack := make([]expressionFrame, 0, 64)
	postorder := make([]celast.Expr, 0, 64)
	if root != nil {
		stack = append(stack, expressionFrame{expr: root})
	}
	maxID := int64(0)
	for len(stack) != 0 {
		row := len(stack) - 1
		frame := stack[row]
		stack = stack[:row]
		if frame.expr == nil {
			continue
		}
		if frame.visited {
			postorder = append(postorder, frame.expr)
			if frame.expr.ID() > maxID {
				maxID = frame.expr.ID()
			}
			continue
		}
		stack = append(stack, expressionFrame{expr: frame.expr, visited: true})
		pushExpressionChildren(&stack, frame.expr)
	}
	spans := make([]public.Span, int(maxID)+1)
	var scratch scratchSizes
	for _, expr := range postorder {
		span := tokenSpan(source, runeToByte, info, expr.ID())
		switch expr.Kind() {
		case celast.CallKind:
			call := expr.AsCall()
			if (call.FunctionName() == operators.LogicalAnd || call.FunctionName() == operators.LogicalOr) && len(call.Args()) > scratch.children {
				scratch.children = len(call.Args())
			}
			if call.IsMemberFunction() {
				span = mergeSpan(span, exprSpan(spans, call.Target()))
			}
			for _, argument := range call.Args() {
				span = mergeSpan(span, exprSpan(spans, argument))
			}
		case celast.SelectKind:
			selection := expr.AsSelect()
			span = mergeSpan(span, exprSpan(spans, selection.Operand()))
			span.End = selectedFieldEnd(source, span.End, selection.FieldName())
		case celast.ListKind:
			list := expr.AsList()
			if list.Size() > scratch.literals {
				scratch.literals = list.Size()
			}
			listStringBytes := 0
			for _, element := range list.Elements() {
				span = mergeSpan(span, exprSpan(spans, element))
				listStringBytes += literalStringBytes(element)
			}
			if listStringBytes > scratch.stringBytes {
				scratch.stringBytes = listStringBytes
			}
			span.End = closingDelimiterEnd(source, span.End, ']')
		case celast.LiteralKind:
			if size := literalStringBytes(expr); size > scratch.stringBytes {
				scratch.stringBytes = size
			}
		}
		if expr.ID() > 0 && int(expr.ID()) < len(spans) {
			spans[expr.ID()] = span
		}
	}
	return postorder, spans, scratch
}

func literalStringBytes(expr celast.Expr) int {
	if expr == nil || expr.Kind() != celast.LiteralKind {
		return 0
	}
	if value, ok := expr.AsLiteral().(celtypes.String); ok {
		return len(value)
	}
	return 0
}

func pushExpressionChildren(stack *[]expressionFrame, expr celast.Expr) {
	switch expr.Kind() {
	case celast.CallKind:
		call := expr.AsCall()
		arguments := call.Args()
		for row := len(arguments) - 1; row >= 0; row-- {
			*stack = append(*stack, expressionFrame{expr: arguments[row]})
		}
		if call.IsMemberFunction() {
			*stack = append(*stack, expressionFrame{expr: call.Target()})
		}
	case celast.SelectKind:
		*stack = append(*stack, expressionFrame{expr: expr.AsSelect().Operand()})
	case celast.ListKind:
		elements := expr.AsList().Elements()
		for row := len(elements) - 1; row >= 0; row-- {
			*stack = append(*stack, expressionFrame{expr: elements[row]})
		}
	case celast.MapKind:
		entries := expr.AsMap().Entries()
		for row := len(entries) - 1; row >= 0; row-- {
			entry := entries[row].AsMapEntry()
			*stack = append(*stack, expressionFrame{expr: entry.Value()}, expressionFrame{expr: entry.Key()})
		}
	case celast.StructKind:
		fields := expr.AsStruct().Fields()
		for row := len(fields) - 1; row >= 0; row-- {
			*stack = append(*stack, expressionFrame{expr: fields[row].AsStructField().Value()})
		}
	case celast.ComprehensionKind:
		comprehension := expr.AsComprehension()
		*stack = append(*stack,
			expressionFrame{expr: comprehension.Result()},
			expressionFrame{expr: comprehension.LoopStep()},
			expressionFrame{expr: comprehension.LoopCondition()},
			expressionFrame{expr: comprehension.AccuInit()},
			expressionFrame{expr: comprehension.IterRange()},
		)
	}
}

func tokenSpan(source []byte, runeToByte []uint32, info *celast.SourceInfo, id int64) public.Span {
	if info == nil || id <= 0 {
		return public.Span{}
	}
	offset, ok := info.GetOffsetRange(id)
	if !ok || offset.Start < 0 || int(offset.Start) >= len(runeToByte) {
		return public.Span{}
	}
	start := runeToByte[offset.Start]
	length := max(int64(offset.Stop)-int64(offset.Start), 0)
	end := min(uint64(start)+uint64(length), uint64(len(source)))
	return public.Span{Start: start, End: uint32(end)}
}

func exprSpan(spans []public.Span, expr celast.Expr) public.Span {
	if expr == nil || expr.ID() <= 0 || int(expr.ID()) >= len(spans) {
		return public.Span{}
	}
	return spans[expr.ID()]
}

func mergeSpan(left, right public.Span) public.Span {
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

func selectedFieldEnd(source []byte, start uint32, field string) uint32 {
	offset := skipCELTrivia(source, int(start))
	if len(source)-offset >= len(field) && string(source[offset:offset+len(field)]) == field {
		return uint32(offset + len(field))
	}
	return start
}

func closingDelimiterEnd(source []byte, start uint32, delimiter byte) uint32 {
	for offset := int(start); offset < len(source); {
		next := skipCELTrivia(source, offset)
		if next != offset {
			offset = next
			continue
		}
		if source[offset] == delimiter {
			return uint32(offset + 1)
		}
		offset++
	}
	return start
}

func skipCELTrivia(source []byte, offset int) int {
	for offset < len(source) {
		switch source[offset] {
		case ' ', '\t', '\r', '\n':
			offset++
			continue
		}
		if offset+1 >= len(source) || source[offset] != '/' || source[offset+1] != '/' {
			return offset
		}
		offset += 2
		for offset < len(source) && source[offset] != '\n' {
			offset++
		}
	}
	return offset
}

func unsupportedDiagnostics(
	postorder []celast.Expr,
	spans []public.Span,
	info *celast.SourceInfo,
	source []byte,
	runeToByte []uint32,
	maximum uint32,
) []public.Diagnostic {
	diagnostics := make([]public.Diagnostic, 0, min(len(postorder), int(maximum)))
	for _, expr := range postorder {
		unsupported := false
		switch expr.Kind() {
		case celast.CallKind:
			call := expr.AsCall()
			unsupported = call.IsMemberFunction() || !supportedFunction(call.FunctionName(), call.Args())
		case celast.ComprehensionKind, celast.MapKind, celast.StructKind:
			unsupported = true
		case celast.ListKind:
			unsupported = len(expr.AsList().OptionalIndices()) != 0
		case celast.SelectKind:
			unsupported = expr.AsSelect().IsTestOnly()
		case celast.LiteralKind:
			switch expr.AsLiteral().(type) {
			case celtypes.Bool, celtypes.Int, celtypes.String:
			default:
				unsupported = true
			}
		}
		if !unsupported {
			continue
		}
		span := exprSpan(spans, expr)
		if expr.Kind() == celast.CallKind {
			call := expr.AsCall()
			span = callNameSpan(source, tokenSpan(source, runeToByte, info, expr.ID()), call.FunctionName())
		}
		diagnostics = append(diagnostics, newDiagnostic(public.CodeUnsupported, span, expr.ID(), 0))
		if uint64(len(diagnostics)) >= uint64(maximum) {
			break
		}
	}
	public.SortDiagnostics(diagnostics)
	return diagnostics
}

func callNameSpan(source []byte, callToken public.Span, function string) public.Span {
	end := int(callToken.Start)
	for end > 0 && (source[end-1] == ' ' || source[end-1] == '\t' || source[end-1] == '\r' || source[end-1] == '\n') {
		end--
	}
	start := end - len(function)
	if start >= 0 && end <= len(source) && string(source[start:end]) == function {
		return public.Span{Start: uint32(start), End: uint32(end)}
	}
	return callToken
}

func supportedFunction(function string, arguments []celast.Expr) bool {
	switch function {
	case operators.LogicalAnd, operators.LogicalOr, operators.LogicalNot,
		operators.Equals, operators.NotEquals, operators.Less, operators.LessEquals,
		operators.Greater, operators.GreaterEquals, operators.In:
		return true
	case operators.Negate:
		return len(arguments) == 1 && arguments[0].Kind() == celast.LiteralKind
	default:
		return false
	}
}

func issueDiagnostics(
	source []byte,
	runeToByte, lineRuneStarts []uint32,
	issues *celgo.Issues,
	defaultCode public.DiagnosticCode,
	maximum uint32,
) []public.Diagnostic {
	errors := issues.Errors()
	diagnostics := make([]public.Diagnostic, 0, min(len(errors), int(maximum)))
	for row, issue := range errors {
		code := defaultCode
		message := strings.ToLower(issue.Message)
		if isLimitIssue(message) {
			code = public.CodeLimit
		}
		span := locationSpan(source, runeToByte, lineRuneStarts, issue.Location.Line(), issue.Location.Column())
		diagnostics = append(diagnostics, newDiagnostic(code, span, int64(row+1), 0))
		if uint64(len(diagnostics)) >= uint64(maximum) {
			break
		}
	}
	if len(diagnostics) == 0 {
		diagnostics = append(diagnostics, newDiagnostic(defaultCode, public.Span{}, 0, 0))
	}
	public.SortDiagnostics(diagnostics)
	return diagnostics
}

func isLimitIssue(message string) bool {
	return strings.Contains(message, "expression code point size exceeds limit") ||
		strings.Contains(message, "expression node count exceeds limit") ||
		strings.Contains(message, "max recursion depth") ||
		strings.Contains(message, "maximum recursion depth") ||
		strings.Contains(message, "expression nesting depth") ||
		strings.Contains(message, "nesting depth limit")
}

func locationSpan(source []byte, runeToByte, lineRuneStarts []uint32, line, column int) public.Span {
	if line < 1 || line > len(lineRuneStarts) || column < 0 {
		return public.Span{}
	}
	runeOffset := uint64(lineRuneStarts[line-1]) + uint64(column)
	if runeOffset >= uint64(len(runeToByte)) {
		return public.Span{Start: uint32(len(source)), End: uint32(len(source))}
	}
	start := runeToByte[runeOffset]
	if int(runeOffset)+1 >= len(runeToByte) {
		return public.Span{Start: start, End: start}
	}
	return public.Span{Start: start, End: runeToByte[runeOffset+1]}
}

func newDiagnostic(code public.DiagnosticCode, span public.Span, row int64, field public.FieldID) public.Diagnostic {
	encodedRow := uint32(0)
	if row > 0 && uint64(row) <= uint64(^uint32(0)) {
		encodedRow = uint32(row)
	}
	return public.Diagnostic{Span: span, Row: encodedRow, Field: field, Language: public.LanguageCEL, Code: code}
}

func cloneBindings(bindings public.BindingSet) public.BindingSet {
	cloned := bindings
	cloned.Fields = append([]public.Binding(nil), bindings.Fields...)
	return cloned
}

func boundBooleanExpression(expr celast.Expr, bindings public.BindingSet) bool {
	_, kind, syntax, found := boundField(expr, bindings)
	return syntax && found && kind == public.ValueKindBoolean
}
