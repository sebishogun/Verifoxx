// Package cedar translates a bounded Cedar subset into the shared semantic
// frontend representation.
package cedar

import (
	"bytes"
	"unicode/utf8"

	cedargo "github.com/cedar-policy/cedar-go"
	cedarast "github.com/cedar-policy/cedar-go/x/exp/ast"

	public "github.com/sebishogun/verifoxx/frontend"
)

// Parsed owns the official Cedar ASTs and source metadata needed for exact
// byte-span translation. Parser objects never leave this package.
type Parsed struct {
	policies cedargo.PolicyList
	syntax   []policySyntax
	tokens   []token
	source   []byte
	bindings public.BindingSet
	limits   public.Limits
}

// Parse parses source with cedar-go and validates the bounded Cedar contract.
func Parse(source []byte, bindings public.BindingSet, limits public.Limits) (*Parsed, []public.Diagnostic) {
	if diagnostics := validateInputs(source, bindings, limits); len(diagnostics) != 0 {
		return nil, diagnostics
	}
	ownedSource := bytes.Clone(source)
	tokens, failure := lex(ownedSource, limits.MaxDepth)
	if failure.Code.Valid() {
		return nil, []public.Diagnostic{newDiagnostic(failure.Code, failure.Span, 0, 0)}
	}
	policies, err := cedargo.NewPolicyListFromBytes("policy.cedar", ownedSource)
	if err != nil {
		return nil, []public.Diagnostic{newDiagnostic(public.CodeSyntax, parserErrorSpan(ownedSource, err.Error()), 0, 0)}
	}
	if len(policies) == 0 {
		return nil, []public.Diagnostic{newDiagnostic(public.CodeInvalidPolicy, public.Span{}, 0, 0)}
	}
	offsets := make([]uint32, len(policies))
	for row, policy := range policies {
		position := policy.Position()
		if position.Offset < 0 || uint64(position.Offset) > uint64(len(source)) {
			return nil, []public.Diagnostic{newDiagnostic(public.CodeInvalidPolicy, public.Span{}, uint32(row+1), 0)}
		}
		offsets[row] = uint32(position.Offset)
	}
	syntax, ok := scanPolicySyntax(tokens, offsets)
	if !ok || len(syntax) != len(policies) {
		return nil, []public.Diagnostic{newDiagnostic(public.CodeInvalidPolicy, public.Span{}, 0, 0)}
	}

	diagnostics := make([]public.Diagnostic, 0, 4)
	totalNodes := uint64(0)
	for row, policy := range policies {
		ast := (*cedarast.Policy)(policy.AST())
		if len(ast.Annotations) != 0 {
			diagnostics = appendBounded(diagnostics, newDiagnostic(public.CodeUnsupported, syntax[row].span, uint32(row+1), 0), limits)
			continue
		}
		if !supportedScope(ast.Principal) || !supportedScope(ast.Action) || !supportedScope(ast.Resource) {
			diagnostics = appendBounded(diagnostics, newDiagnostic(public.CodeUnsupported, syntax[row].span, uint32(row+1), 0), limits)
			continue
		}
		if len(ast.Conditions) != len(syntax[row].conditions) {
			diagnostics = appendBounded(diagnostics, newDiagnostic(public.CodeInvalidPolicy, syntax[row].span, uint32(row+1), 0), limits)
			continue
		}
		policyNodes := uint64(scopeNodeCount(ast))
		policyLimited := false
		for conditionRow, condition := range ast.Conditions {
			nodes, depth, supported := inspectExpression(condition.Body)
			policyNodes += uint64(nodes)
			conditionSpan := syntax[row].conditions[conditionRow].span
			if !supported {
				diagnostics = appendBounded(diagnostics, newDiagnostic(public.CodeUnsupported, conditionSpan, uint32(row+1), 0), limits)
				continue
			}
			if uint64(depth) > uint64(limits.MaxDepth) {
				diagnostics = appendBounded(diagnostics, newDiagnostic(public.CodeLimit, conditionSpan, uint32(row+1), 0), limits)
				policyLimited = true
				continue
			}
			if _, ok := parseExpression(tokens, syntax[row].conditions[conditionRow].tokenStart, syntax[row].conditions[conditionRow].tokenEnd); !ok {
				diagnostics = appendBounded(diagnostics, newDiagnostic(public.CodeInvalidPolicy, conditionSpan, uint32(row+1), 0), limits)
			}
		}
		totalNodes += policyNodes
		if totalNodes > uint64(limits.MaxNodes) && !policyLimited {
			diagnostics = appendBounded(diagnostics, newDiagnostic(public.CodeLimit, syntax[row].span, uint32(row+1), 0), limits)
		}
	}
	if len(diagnostics) != 0 {
		public.SortDiagnostics(diagnostics)
		return nil, diagnostics
	}

	return &Parsed{
		policies: policies, syntax: syntax, tokens: tokens, source: ownedSource,
		bindings: cloneBindings(bindings), limits: limits,
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

func supportedScope(scope cedarast.IsScopeNode) bool {
	switch scope.(type) {
	case cedarast.ScopeTypeAll, cedarast.ScopeTypeEq:
		return true
	default:
		return false
	}
}

func inspectExpression(node cedarast.IsNode) (nodes, depth uint32, supported bool) {
	if node == nil {
		return 0, 0, false
	}
	switch value := node.(type) {
	case cedarast.NodeValue:
		if _, ok := scalarValue(value.Value); !ok {
			return 1, 1, false
		}
		return 1, 1, true
	case cedarast.NodeTypeAccess:
		_, ok := contextPath(value)
		return 1, 1, ok
	case cedarast.NodeTypeAnd:
		return inspectBinary(value.BinaryNode)
	case cedarast.NodeTypeOr:
		return inspectBinary(value.BinaryNode)
	case cedarast.NodeTypeEquals:
		return inspectBinary(value.BinaryNode)
	case cedarast.NodeTypeNotEquals:
		return inspectBinary(value.BinaryNode)
	case cedarast.NodeTypeLessThan:
		return inspectBinary(value.BinaryNode)
	case cedarast.NodeTypeLessThanOrEqual:
		return inspectBinary(value.BinaryNode)
	case cedarast.NodeTypeGreaterThan:
		return inspectBinary(value.BinaryNode)
	case cedarast.NodeTypeGreaterThanOrEqual:
		return inspectBinary(value.BinaryNode)
	case cedarast.NodeTypeNot:
		childNodes, childDepth, ok := inspectExpression(value.Arg)
		return childNodes + 1, childDepth + 1, ok
	default:
		return 1, 1, false
	}
}

func inspectBinary(node cedarast.BinaryNode) (nodes, depth uint32, supported bool) {
	leftNodes, leftDepth, leftOK := inspectExpression(node.Left)
	rightNodes, rightDepth, rightOK := inspectExpression(node.Right)
	return leftNodes + rightNodes + 1, max(leftDepth, rightDepth) + 1, leftOK && rightOK
}

func scopeNodeCount(policy *cedarast.Policy) uint32 {
	count := uint32(0)
	for _, scope := range []cedarast.IsScopeNode{policy.Principal, policy.Action, policy.Resource} {
		if _, ok := scope.(cedarast.ScopeTypeEq); ok {
			count++
		}
	}
	return count
}

func appendBounded(diagnostics []public.Diagnostic, diagnostic public.Diagnostic, limits public.Limits) []public.Diagnostic {
	if uint64(len(diagnostics)) < uint64(limits.MaxDiagnostics) {
		return append(diagnostics, diagnostic)
	}
	return diagnostics
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

func cloneBindings(bindings public.BindingSet) public.BindingSet {
	result := bindings
	result.Fields = append([]public.Binding(nil), bindings.Fields...)
	return result
}

func newDiagnostic(code public.DiagnosticCode, span public.Span, row uint32, field public.FieldID) public.Diagnostic {
	return public.Diagnostic{Span: span, Row: row, Field: field, Language: public.LanguageCedar, Code: code}
}
