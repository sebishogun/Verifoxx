package cedar

import (
	"bytes"
	"errors"

	cedartypes "github.com/cedar-policy/cedar-go/types"
	cedarast "github.com/cedar-policy/cedar-go/x/exp/ast"

	public "github.com/sebishogun/verifoxx/frontend"
)

type lowerResult struct {
	node  public.NodeID
	field public.FieldID
	code  public.DiagnosticCode
}

// Lower translates checked Cedar policies into one owned semantic policy.
func Lower(source []byte, parsed *Parsed, bindings public.BindingSet, limits public.Limits) (*public.Policy, []public.Diagnostic) {
	if !matchingParseInputs(source, parsed, bindings, limits) {
		return nil, []public.Diagnostic{newDiagnostic(public.CodeInvalidPolicy, public.Span{}, 0, 0)}
	}
	builder, err := public.NewBuilder(source, bindings, limits)
	if err != nil {
		return nil, []public.Diagnostic{newDiagnostic(builderErrorCode(err), public.Span{}, 0, 0)}
	}
	permits := make([]public.NodeID, 0, len(parsed.policies))
	forbids := make([]public.NodeID, 0, len(parsed.policies))
	diagnostics := make([]public.Diagnostic, 0, 4)
	for row, policy := range parsed.policies {
		ast := (*cedarast.Policy)(policy.AST())
		parts := make([]public.NodeID, 0, 3+len(ast.Conditions))
		scopes := []struct {
			name  string
			scope cedarast.IsScopeNode
			span  public.Span
		}{
			{name: "principal", scope: ast.Principal, span: parsed.syntax[row].scopeSpans[0]},
			{name: "action", scope: ast.Action, span: parsed.syntax[row].scopeSpans[1]},
			{name: "resource", scope: ast.Resource, span: parsed.syntax[row].scopeSpans[2]},
		}
		for _, scope := range scopes {
			result := lowerScope(builder, scope.name, scope.scope, scope.span, bindings)
			if result.code.Valid() {
				diagnostics = appendBounded(diagnostics, newDiagnostic(result.code, scope.span, uint32(row+1), result.field), limits)
				continue
			}
			if result.node != 0 {
				parts = append(parts, result.node)
			}
		}
		for conditionRow, condition := range ast.Conditions {
			syntax, ok := parseExpression(parsed.tokens, parsed.syntax[row].conditions[conditionRow].tokenStart, parsed.syntax[row].conditions[conditionRow].tokenEnd)
			if !ok {
				diagnostics = appendBounded(diagnostics, newDiagnostic(public.CodeInvalidPolicy, parsed.syntax[row].conditions[conditionRow].span, uint32(row+1), 0), limits)
				continue
			}
			result := lowerExpression(builder, condition.Body, syntax, bindings)
			if result.code.Valid() {
				diagnostics = appendBounded(diagnostics, newDiagnostic(result.code, syntaxSpan(syntax), uint32(row+1), result.field), limits)
				continue
			}
			node := result.node
			if condition.Condition == cedarast.ConditionUnless {
				node, err = builder.AddNot(node, syntax.span)
				if err != nil {
					diagnostics = appendBounded(diagnostics, newDiagnostic(builderErrorCode(err), syntax.span, uint32(row+1), result.field), limits)
					continue
				}
			}
			parts = append(parts, node)
		}
		if len(diagnostics) != 0 {
			continue
		}
		policyNode, code := combineAll(builder, parts, parsed.syntax[row].span)
		if code.Valid() {
			diagnostics = appendBounded(diagnostics, newDiagnostic(code, parsed.syntax[row].span, uint32(row+1), 0), limits)
			continue
		}
		if ast.Effect == cedarast.EffectPermit {
			permits = append(permits, policyNode)
		} else {
			forbids = append(forbids, policyNode)
		}
	}
	if len(diagnostics) != 0 {
		public.SortDiagnostics(diagnostics)
		return nil, diagnostics
	}

	rootSpan := public.Span{End: uint32(len(source))}
	permitRoot, code := combineAny(builder, permits, rootSpan)
	if code.Valid() {
		return nil, []public.Diagnostic{newDiagnostic(code, rootSpan, 0, 0)}
	}
	root := permitRoot
	if len(forbids) != 0 {
		forbidRoot, code := combineAny(builder, forbids, rootSpan)
		if code.Valid() {
			return nil, []public.Diagnostic{newDiagnostic(code, rootSpan, 0, 0)}
		}
		notForbid, err := builder.AddNot(forbidRoot, rootSpan)
		if err != nil {
			return nil, []public.Diagnostic{newDiagnostic(builderErrorCode(err), rootSpan, 0, 0)}
		}
		root, err = builder.AddAll([]public.NodeID{permitRoot, notForbid}, rootSpan)
		if err != nil {
			return nil, []public.Diagnostic{newDiagnostic(builderErrorCode(err), rootSpan, 0, 0)}
		}
	}
	policy, err := builder.Finish(root, public.DefaultEscalate)
	if err != nil {
		return nil, []public.Diagnostic{newDiagnostic(builderErrorCode(err), rootSpan, 0, 0)}
	}
	return policy, nil
}

// Compile parses and lowers one Cedar policy document.
func Compile(source []byte, bindings public.BindingSet, limits public.Limits) (*public.Policy, []public.Diagnostic) {
	parsed, diagnostics := Parse(source, bindings, limits)
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}
	return Lower(source, parsed, bindings, limits)
}

func matchingParseInputs(source []byte, parsed *Parsed, bindings public.BindingSet, limits public.Limits) bool {
	return parsed != nil && len(parsed.policies) != 0 && limits.Valid() && limits == parsed.limits &&
		bytes.Equal(source, parsed.source) && equalBindings(bindings, parsed.bindings)
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

func lowerScope(builder *public.Builder, name string, scope cedarast.IsScopeNode, span public.Span, bindings public.BindingSet) lowerResult {
	switch scope := scope.(type) {
	case cedarast.ScopeTypeAll:
		return lowerResult{}
	case cedarast.ScopeTypeEq:
		field, kind, found := boundPath(name, bindings)
		if !found {
			return lowerResult{code: public.CodeUnknownField}
		}
		if kind != public.ValueKindString {
			return lowerResult{field: field, code: public.CodeType}
		}
		node, err := builder.AddCompare(field, public.CompareOpEqual, public.StringLiteral([]byte(scope.Entity.String())), span)
		return lowerResult{node: node, field: field, code: builderErrorCode(err)}
	default:
		return lowerResult{code: public.CodeUnsupported}
	}
}

func lowerExpression(builder *public.Builder, node cedarast.IsNode, syntax *syntaxExpr, bindings public.BindingSet) lowerResult {
	switch value := node.(type) {
	case cedarast.NodeValue:
		literal, ok := scalarValue(value.Value)
		if !ok || literal.Kind != public.ValueKindBoolean || syntax.kind != syntaxLiteral {
			return lowerResult{code: public.CodeUnsupported}
		}
		node, err := builder.AddBoolean(literal.Boolean, syntax.span)
		return lowerResult{node: node, code: builderErrorCode(err)}
	case cedarast.NodeTypeAccess:
		path, ok := contextPath(value)
		if !ok || syntax.kind != syntaxPath || syntax.path != path {
			return lowerResult{code: public.CodeInvalidPolicy}
		}
		field, kind, found := boundPath(path, bindings)
		if !found {
			return lowerResult{code: public.CodeUnknownField}
		}
		if kind != public.ValueKindBoolean {
			return lowerResult{field: field, code: public.CodeType}
		}
		result, err := builder.AddCompare(field, public.CompareOpEqual, public.BooleanLiteral(true), syntax.span)
		return lowerResult{node: result, field: field, code: builderErrorCode(err)}
	case cedarast.NodeTypeAnd:
		return lowerBinaryGroup(builder, value.BinaryNode, syntax, bindings, true)
	case cedarast.NodeTypeOr:
		return lowerBinaryGroup(builder, value.BinaryNode, syntax, bindings, false)
	case cedarast.NodeTypeNot:
		if syntax.kind != syntaxNot {
			return lowerResult{code: public.CodeInvalidPolicy}
		}
		child := lowerExpression(builder, value.Arg, syntax.left, bindings)
		if child.code.Valid() {
			return child
		}
		result, err := builder.AddNot(child.node, syntax.span)
		return lowerResult{node: result, field: child.field, code: builderErrorCode(err)}
	case cedarast.NodeTypeEquals:
		return lowerComparison(builder, value.BinaryNode, public.CompareOpEqual, syntax, bindings)
	case cedarast.NodeTypeNotEquals:
		return lowerComparison(builder, value.BinaryNode, public.CompareOpNotEqual, syntax, bindings)
	case cedarast.NodeTypeLessThan:
		return lowerComparison(builder, value.BinaryNode, public.CompareOpLess, syntax, bindings)
	case cedarast.NodeTypeLessThanOrEqual:
		return lowerComparison(builder, value.BinaryNode, public.CompareOpLessEqual, syntax, bindings)
	case cedarast.NodeTypeGreaterThan:
		return lowerComparison(builder, value.BinaryNode, public.CompareOpGreater, syntax, bindings)
	case cedarast.NodeTypeGreaterThanOrEqual:
		return lowerComparison(builder, value.BinaryNode, public.CompareOpGreaterEqual, syntax, bindings)
	default:
		return lowerResult{code: public.CodeUnsupported}
	}
}

func lowerBinaryGroup(builder *public.Builder, binary cedarast.BinaryNode, syntax *syntaxExpr, bindings public.BindingSet, all bool) lowerResult {
	want := syntaxOr
	if all {
		want = syntaxAnd
	}
	if syntax.kind != want {
		return lowerResult{code: public.CodeInvalidPolicy}
	}
	left := lowerExpression(builder, binary.Left, syntax.left, bindings)
	if left.code.Valid() {
		return left
	}
	right := lowerExpression(builder, binary.Right, syntax.right, bindings)
	if right.code.Valid() {
		return right
	}
	var (
		node public.NodeID
		err  error
	)
	if all {
		node, err = builder.AddAll([]public.NodeID{left.node, right.node}, syntax.span)
	} else {
		node, err = builder.AddAny([]public.NodeID{left.node, right.node}, syntax.span)
	}
	return lowerResult{node: node, code: builderErrorCode(err)}
}

func lowerComparison(builder *public.Builder, binary cedarast.BinaryNode, operation public.CompareOp, syntax *syntaxExpr, bindings public.BindingSet) lowerResult {
	if syntax.kind != syntaxCompare || syntaxOperation(syntax.op) != operation {
		return lowerResult{code: public.CodeInvalidPolicy}
	}
	leftPath, leftIsPath := expressionPath(binary.Left)
	rightPath, rightIsPath := expressionPath(binary.Right)
	if leftIsPath == rightIsPath {
		return lowerResult{code: public.CodeUnsupported}
	}
	fieldPath := leftPath
	literalNode := binary.Right
	fieldSyntax := syntax.left
	if rightIsPath {
		fieldPath = rightPath
		literalNode = binary.Left
		fieldSyntax = syntax.right
		operation = reverseOperation(operation)
	}
	if fieldSyntax == nil || fieldSyntax.kind != syntaxPath || fieldSyntax.path != fieldPath {
		return lowerResult{code: public.CodeInvalidPolicy}
	}
	field, kind, found := boundPath(fieldPath, bindings)
	if !found {
		return lowerResult{code: public.CodeUnknownField}
	}
	value, ok := literalNode.(cedarast.NodeValue)
	if !ok {
		return lowerResult{field: field, code: public.CodeUnsupported}
	}
	literal, ok := scalarValue(value.Value)
	if !ok {
		return lowerResult{field: field, code: public.CodeUnsupported}
	}
	if literal.Kind != kind {
		return lowerResult{field: field, code: public.CodeType}
	}
	if orderedOperation(operation) && kind != public.ValueKindInteger {
		code := public.CodeType
		if kind == public.ValueKindString {
			code = public.CodeUnsupported
		}
		return lowerResult{field: field, code: code}
	}
	node, err := builder.AddCompare(field, operation, literal, syntax.span)
	return lowerResult{node: node, field: field, code: builderErrorCode(err)}
}

func combineAll(builder *public.Builder, nodes []public.NodeID, span public.Span) (public.NodeID, public.DiagnosticCode) {
	switch len(nodes) {
	case 0:
		node, err := builder.AddBoolean(true, span)
		return node, builderErrorCode(err)
	case 1:
		return nodes[0], public.CodeInvalid
	default:
		node, err := builder.AddAll(nodes, span)
		return node, builderErrorCode(err)
	}
}

func combineAny(builder *public.Builder, nodes []public.NodeID, span public.Span) (public.NodeID, public.DiagnosticCode) {
	switch len(nodes) {
	case 0:
		node, err := builder.AddBoolean(false, span)
		return node, builderErrorCode(err)
	case 1:
		return nodes[0], public.CodeInvalid
	default:
		node, err := builder.AddAny(nodes, span)
		return node, builderErrorCode(err)
	}
}

func contextPath(access cedarast.NodeTypeAccess) (string, bool) {
	parts := make([]string, 0, 4)
	var node cedarast.IsNode = access
	for {
		switch value := node.(type) {
		case cedarast.NodeTypeAccess:
			parts = append(parts, string(value.Value))
			node = value.Arg
		case cedarast.NodeTypeVariable:
			if string(value.Name) != "context" || len(parts) == 0 {
				return "", false
			}
			path := "context"
			for row := len(parts) - 1; row >= 0; row-- {
				path += "." + parts[row]
			}
			return path, true
		default:
			return "", false
		}
	}
}

func expressionPath(node cedarast.IsNode) (string, bool) {
	access, ok := node.(cedarast.NodeTypeAccess)
	if !ok {
		return "", false
	}
	return contextPath(access)
}

func boundPath(path string, bindings public.BindingSet) (public.FieldID, public.ValueKind, bool) {
	for row := range bindings.Fields {
		if bindings.Fields[row].Source == path {
			return public.FieldID(row + 1), bindings.Fields[row].Kind, true
		}
	}
	return 0, public.ValueKindInvalid, false
}

func scalarValue(value cedartypes.Value) (public.Literal, bool) {
	switch value := value.(type) {
	case cedartypes.String:
		return public.StringLiteral([]byte(value)), true
	case cedartypes.Long:
		return public.IntegerLiteral(int64(value)), true
	case cedartypes.Boolean:
		return public.BooleanLiteral(bool(value)), true
	default:
		return public.Literal{}, false
	}
}

func syntaxOperation(operation string) public.CompareOp {
	switch operation {
	case "==":
		return public.CompareOpEqual
	case "!=":
		return public.CompareOpNotEqual
	case "<":
		return public.CompareOpLess
	case "<=":
		return public.CompareOpLessEqual
	case ">":
		return public.CompareOpGreater
	case ">=":
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

func orderedOperation(operation public.CompareOp) bool {
	return operation >= public.CompareOpLess && operation <= public.CompareOpGreaterEqual
}

func builderErrorCode(err error) public.DiagnosticCode {
	switch {
	case err == nil:
		return public.CodeInvalid
	case errors.Is(err, public.ErrLimitExceeded):
		return public.CodeLimit
	case errors.Is(err, public.ErrInvalidField):
		return public.CodeUnknownField
	case errors.Is(err, public.ErrInvalidLiteral), errors.Is(err, public.ErrInvalidOperation):
		return public.CodeType
	default:
		return public.CodeInvalidPolicy
	}
}
