package cel

import (
	"bytes"
	"errors"
	"math"

	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	celtypes "cel.dev/cel-go/common/types"

	public "github.com/sebishogun/verifoxx/frontend"
)

type lowerFrame struct {
	expr    celast.Expr
	visited bool
}

type lowerScratch struct {
	children []public.NodeID
	literals []public.Literal
	strings  []byte
}

type lowerResult struct {
	span  public.Span
	node  public.NodeID
	field public.FieldID
	code  public.DiagnosticCode
}

// Lower translates a checked CEL tree into an owned semantic policy.
func Lower(source []byte, parsed *Parsed, bindings public.BindingSet, limits public.Limits) (*public.Policy, []public.Diagnostic) {
	if !matchingParseInputs(source, parsed, bindings, limits) {
		return nil, []public.Diagnostic{newDiagnostic(public.CodeInvalidPolicy, public.Span{}, 0, 0)}
	}
	builder, err := public.NewBuilder(source, bindings, limits)
	if err != nil {
		return nil, []public.Diagnostic{newDiagnostic(builderErrorCode(err), public.Span{}, 0, 0)}
	}
	rootExpr := parsed.ast.NativeRep().Expr()
	nodeIDs := make([]public.NodeID, len(parsed.exprSpans))
	scratch := lowerScratch{
		children: make([]public.NodeID, parsed.scratch.children),
		literals: make([]public.Literal, parsed.scratch.literals),
		strings:  make([]byte, 0, parsed.scratch.stringBytes),
	}
	stack := make([]lowerFrame, 0, min(len(parsed.exprSpans), 128))
	stack = append(stack, lowerFrame{expr: rootExpr})
	diagnostics := make([]public.Diagnostic, 0, 4)
	for len(stack) != 0 && uint64(len(diagnostics)) < uint64(limits.MaxDiagnostics) {
		row := len(stack) - 1
		frame := stack[row]
		stack = stack[:row]
		if frame.expr == nil {
			continue
		}
		if !frame.visited {
			stack = append(stack, lowerFrame{expr: frame.expr, visited: true})
			pushLowerChildren(&stack, frame.expr)
			continue
		}
		result := lowerExpression(builder, frame.expr, nodeIDs, parsed.exprSpans, bindings, &scratch)
		if result.code.Valid() {
			span := result.span
			if span.Start == span.End {
				span = exprSpan(parsed.exprSpans, frame.expr)
			}
			diagnostics = append(diagnostics, newDiagnostic(result.code, span, frame.expr.ID(), result.field))
			continue
		}
		if frame.expr.ID() > 0 && int(frame.expr.ID()) < len(nodeIDs) {
			nodeIDs[frame.expr.ID()] = result.node
		}
	}
	if len(diagnostics) != 0 {
		public.SortDiagnostics(diagnostics)
		return nil, diagnostics
	}
	if rootExpr.ID() <= 0 || int(rootExpr.ID()) >= len(nodeIDs) || nodeIDs[rootExpr.ID()] == 0 {
		return nil, []public.Diagnostic{newDiagnostic(public.CodeInvalidPolicy, exprSpan(parsed.exprSpans, rootExpr), rootExpr.ID(), 0)}
	}
	policy, err := builder.Finish(nodeIDs[rootExpr.ID()], public.DefaultEscalate)
	if err != nil {
		return nil, []public.Diagnostic{newDiagnostic(builderErrorCode(err), exprSpan(parsed.exprSpans, rootExpr), rootExpr.ID(), 0)}
	}
	return policy, nil
}

// Compile parses, checks, and lowers one CEL policy.
func Compile(source []byte, bindings public.BindingSet, limits public.Limits) (*public.Policy, []public.Diagnostic) {
	parsed, diagnostics := Parse(source, bindings, limits)
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}
	return Lower(source, parsed, bindings, limits)
}

func matchingParseInputs(source []byte, parsed *Parsed, bindings public.BindingSet, limits public.Limits) bool {
	return parsed != nil && parsed.ast != nil && parsed.ast.NativeRep() != nil && limits.Valid() &&
		limits == parsed.limits && bytes.Equal(source, parsed.source) && equalBindings(bindings, parsed.bindings)
}

func equalBindings(left, right public.BindingSet) bool {
	if left.Name != right.Name || left.Version != right.Version || left.Decision != right.Decision || len(left.Fields) != len(right.Fields) {
		return false
	}
	for row := range left.Fields {
		if left.Fields[row] != right.Fields[row] {
			return false
		}
	}
	return true
}

func pushLowerChildren(stack *[]lowerFrame, expr celast.Expr) {
	if expr.Kind() != celast.CallKind {
		return
	}
	call := expr.AsCall()
	arguments := call.Args()
	switch call.FunctionName() {
	case operators.LogicalAnd, operators.LogicalOr:
		for row := len(arguments) - 1; row >= 0; row-- {
			*stack = append(*stack, lowerFrame{expr: arguments[row]})
		}
	case operators.LogicalNot:
		if len(arguments) == 1 {
			*stack = append(*stack, lowerFrame{expr: arguments[0]})
		}
	}
}

func lowerExpression(
	builder *public.Builder,
	expr celast.Expr,
	nodeIDs []public.NodeID,
	spans []public.Span,
	bindings public.BindingSet,
	scratch *lowerScratch,
) lowerResult {
	span := exprSpan(spans, expr)
	switch expr.Kind() {
	case celast.LiteralKind:
		value, ok := expr.AsLiteral().(celtypes.Bool)
		if !ok {
			return failedLower(public.CodeUnsupported, 0, public.Span{})
		}
		node, err := builder.AddBoolean(bool(value), span)
		return completedLower(node, err, 0)
	case celast.IdentKind, celast.SelectKind:
		field, kind, syntax, found := boundField(expr, bindings)
		if !syntax {
			return failedLower(public.CodeUnsupported, 0, public.Span{})
		}
		if !found {
			return failedLower(public.CodeUnknownField, 0, span)
		}
		if kind != public.ValueKindBoolean {
			return failedLower(public.CodeType, field, public.Span{})
		}
		node, err := builder.AddCompare(field, public.CompareOpEqual, public.BooleanLiteral(true), span)
		return completedLower(node, err, field)
	case celast.CallKind:
		return lowerCall(builder, expr, nodeIDs, spans, bindings, scratch)
	default:
		return failedLower(public.CodeUnsupported, 0, public.Span{})
	}
}

func lowerCall(
	builder *public.Builder,
	expr celast.Expr,
	nodeIDs []public.NodeID,
	spans []public.Span,
	bindings public.BindingSet,
	scratch *lowerScratch,
) lowerResult {
	call := expr.AsCall()
	arguments := call.Args()
	span := exprSpan(spans, expr)
	if call.IsMemberFunction() {
		return failedLower(public.CodeUnsupported, 0, public.Span{})
	}
	switch call.FunctionName() {
	case operators.LogicalAnd, operators.LogicalOr:
		if len(arguments) < 2 || len(arguments) > math.MaxUint16 {
			return failedLower(public.CodeUnsupported, 0, public.Span{})
		}
		children := scratch.children[:len(arguments)]
		for row, argument := range arguments {
			if argument.ID() <= 0 || int(argument.ID()) >= len(nodeIDs) || nodeIDs[argument.ID()] == 0 {
				return lowerResult{}
			}
			children[row] = nodeIDs[argument.ID()]
		}
		var (
			node public.NodeID
			err  error
		)
		if call.FunctionName() == operators.LogicalAnd {
			node, err = builder.AddAll(children, span)
		} else {
			node, err = builder.AddAny(children, span)
		}
		return completedLower(node, err, 0)
	case operators.LogicalNot:
		if len(arguments) != 1 || arguments[0].ID() <= 0 || int(arguments[0].ID()) >= len(nodeIDs) || nodeIDs[arguments[0].ID()] == 0 {
			return lowerResult{}
		}
		node, err := builder.AddNot(nodeIDs[arguments[0].ID()], span)
		return completedLower(node, err, 0)
	case operators.In:
		return lowerMembership(builder, arguments, span, spans, bindings, scratch)
	case operators.Equals, operators.NotEquals, operators.Less, operators.LessEquals, operators.Greater, operators.GreaterEquals:
		return lowerComparison(builder, call.FunctionName(), arguments, span, spans, bindings, scratch)
	default:
		return failedLower(public.CodeUnsupported, 0, public.Span{})
	}
}

func lowerComparison(
	builder *public.Builder,
	function string,
	arguments []celast.Expr,
	span public.Span,
	spans []public.Span,
	bindings public.BindingSet,
	scratch *lowerScratch,
) lowerResult {
	if len(arguments) != 2 {
		return failedLower(public.CodeUnsupported, 0, public.Span{})
	}
	leftField, _, leftSyntax, leftFound := boundField(arguments[0], bindings)
	rightField, _, rightSyntax, rightFound := boundField(arguments[1], bindings)
	if leftSyntax && !leftFound {
		return failedLower(public.CodeUnknownField, 0, exprSpan(spans, arguments[0]))
	}
	if rightSyntax && !rightFound {
		return failedLower(public.CodeUnknownField, 0, exprSpan(spans, arguments[1]))
	}
	if leftSyntax && rightSyntax {
		return failedLower(public.CodeUnsupported, leftField, public.Span{})
	}
	operation := compareOperation(function)
	if !operation.Valid() {
		return failedLower(public.CodeUnsupported, 0, public.Span{})
	}
	if leftSyntax {
		scratch.strings = scratch.strings[:0]
		literal, ok := scalarLiteral(arguments[1], &scratch.strings)
		if !ok {
			return failedLower(public.CodeUnsupported, leftField, public.Span{})
		}
		node, err := builder.AddCompare(leftField, operation, literal, span)
		return completedLower(node, err, leftField)
	}
	if rightSyntax {
		scratch.strings = scratch.strings[:0]
		literal, ok := scalarLiteral(arguments[0], &scratch.strings)
		if !ok {
			return failedLower(public.CodeUnsupported, rightField, public.Span{})
		}
		node, err := builder.AddCompare(rightField, reverseOperation(operation), literal, span)
		return completedLower(node, err, rightField)
	}
	return failedLower(public.CodeUnsupported, 0, public.Span{})
}

func lowerMembership(
	builder *public.Builder,
	arguments []celast.Expr,
	span public.Span,
	spans []public.Span,
	bindings public.BindingSet,
	scratch *lowerScratch,
) lowerResult {
	if len(arguments) != 2 {
		return failedLower(public.CodeUnsupported, 0, public.Span{})
	}
	field, _, syntax, found := boundField(arguments[0], bindings)
	if !syntax {
		return failedLower(public.CodeUnsupported, 0, public.Span{})
	}
	if !found {
		return failedLower(public.CodeUnknownField, 0, exprSpan(spans, arguments[0]))
	}
	if arguments[1].Kind() != celast.ListKind {
		return failedLower(public.CodeUnsupported, field, public.Span{})
	}
	list := arguments[1].AsList()
	if list.Size() == 0 || len(list.OptionalIndices()) != 0 || list.Size() > math.MaxUint16 {
		return failedLower(public.CodeUnsupported, field, public.Span{})
	}
	literals := scratch.literals[:list.Size()]
	scratch.strings = scratch.strings[:0]
	for row, element := range list.Elements() {
		literal, ok := scalarLiteral(element, &scratch.strings)
		if !ok {
			return failedLower(public.CodeUnsupported, field, public.Span{})
		}
		literals[row] = literal
	}
	node, err := builder.AddIn(field, literals, span)
	return completedLower(node, err, field)
}

func completedLower(node public.NodeID, err error, field public.FieldID) lowerResult {
	return lowerResult{node: node, field: field, code: builderErrorCode(err)}
}

func failedLower(code public.DiagnosticCode, field public.FieldID, span public.Span) lowerResult {
	return lowerResult{span: span, field: field, code: code}
}

func boundField(expr celast.Expr, bindings public.BindingSet) (public.FieldID, public.ValueKind, bool, bool) {
	if !isFieldExpression(expr) {
		return 0, public.ValueKindInvalid, false, false
	}
	for row := range bindings.Fields {
		if pathMatches(expr, bindings.Fields[row].Source) {
			return public.FieldID(row + 1), bindings.Fields[row].Kind, true, true
		}
	}
	return 0, public.ValueKindInvalid, true, false
}

func isFieldExpression(expr celast.Expr) bool {
	for expr != nil {
		switch expr.Kind() {
		case celast.IdentKind:
			return true
		case celast.SelectKind:
			selection := expr.AsSelect()
			if selection.IsTestOnly() {
				return false
			}
			expr = selection.Operand()
		default:
			return false
		}
	}
	return false
}

func pathMatches(expr celast.Expr, path string) bool {
	for expr != nil {
		switch expr.Kind() {
		case celast.IdentKind:
			return path == expr.AsIdent()
		case celast.SelectKind:
			selection := expr.AsSelect()
			field := selection.FieldName()
			if len(path) <= len(field) || path[len(path)-len(field)-1] != '.' || path[len(path)-len(field):] != field {
				return false
			}
			path = path[:len(path)-len(field)-1]
			expr = selection.Operand()
		default:
			return false
		}
	}
	return false
}

func scalarLiteral(expr celast.Expr, stringScratch *[]byte) (public.Literal, bool) {
	if expr == nil {
		return public.Literal{}, false
	}
	if expr.Kind() == celast.CallKind {
		call := expr.AsCall()
		arguments := call.Args()
		if call.IsMemberFunction() || call.FunctionName() != operators.Negate || len(arguments) != 1 || arguments[0].Kind() != celast.LiteralKind {
			return public.Literal{}, false
		}
		value, ok := arguments[0].AsLiteral().(celtypes.Int)
		if !ok || int64(value) == math.MinInt64 {
			return public.Literal{}, false
		}
		return public.IntegerLiteral(-int64(value)), true
	}
	if expr.Kind() != celast.LiteralKind {
		return public.Literal{}, false
	}
	switch value := expr.AsLiteral().(type) {
	case celtypes.Bool:
		return public.BooleanLiteral(bool(value)), true
	case celtypes.Int:
		return public.IntegerLiteral(int64(value)), true
	case celtypes.String:
		start := len(*stringScratch)
		*stringScratch = append(*stringScratch, string(value)...)
		return public.StringLiteral((*stringScratch)[start:]), true
	default:
		return public.Literal{}, false
	}
}

func compareOperation(function string) public.CompareOp {
	switch function {
	case operators.Equals:
		return public.CompareOpEqual
	case operators.NotEquals:
		return public.CompareOpNotEqual
	case operators.Less:
		return public.CompareOpLess
	case operators.LessEquals:
		return public.CompareOpLessEqual
	case operators.Greater:
		return public.CompareOpGreater
	case operators.GreaterEquals:
		return public.CompareOpGreaterEqual
	default:
		return public.CompareOpInvalid
	}
}

func reverseOperation(operation public.CompareOp) public.CompareOp {
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

func builderErrorCode(err error) public.DiagnosticCode {
	if err == nil {
		return public.CodeInvalid
	}
	switch {
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
