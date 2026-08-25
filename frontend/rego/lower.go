package rego

import (
	"bytes"
	"errors"
	"math"
	"strings"

	opaast "github.com/open-policy-agent/opa/v1/ast"

	public "github.com/sebishogun/verifoxx/frontend"
)

type lowerResult struct {
	span                  public.Span
	node                  public.NodeID
	field                 public.FieldID
	code                  public.DiagnosticCode
	succeedsWhenUndefined bool
}

type lowerScratch struct {
	children []public.NodeID
	literals []public.Literal
}

// Lower translates a parsed Rego module into an owned semantic policy.
func Lower(source []byte, parsed *Parsed, bindings public.BindingSet, limits public.Limits) (*public.Policy, []public.Diagnostic) {
	if !matchingParseInputs(source, parsed, bindings, limits) {
		return nil, []public.Diagnostic{newDiagnostic(public.CodeInvalidPolicy, public.Span{}, 0, 0)}
	}
	builder, err := public.NewBuilder(source, bindings, limits)
	if err != nil {
		return nil, []public.Diagnostic{newDiagnostic(builderErrorCode(err), public.Span{}, 0, 0)}
	}
	diagnostics := make([]public.Diagnostic, 0, min(4, int(limits.MaxDiagnostics)))
	roots := make([]public.NodeID, 0, len(parsed.module.Rules))
	rootSpans := make([]public.Span, 0, len(parsed.module.Rules))
	ruleNodes := make([]public.NodeID, 0, 8)
	scratch := lowerScratch{children: make([]public.NodeID, 2), literals: make([]public.Literal, 0, 8)}
	var defaultRule *opaast.Rule
	defaultValue := false

	for ruleRow, rule := range parsed.module.Rules {
		if uint64(len(diagnostics)) >= uint64(limits.MaxDiagnostics) {
			break
		}
		span := locationSpan(source, rule.Location)
		code := validateRuleHead(rule, bindings.Decision)
		if code.Valid() {
			diagnostics = append(diagnostics, newDiagnostic(code, span, uint32(ruleRow+1), 0))
			continue
		}
		value, _ := rule.Head.Value.Value.(opaast.Boolean)
		if rule.Default {
			if defaultRule != nil {
				diagnostics = append(diagnostics, newDiagnostic(public.CodeDuplicate, span, uint32(ruleRow+1), 0))
				continue
			}
			defaultRule, defaultValue = rule, bool(value)
			continue
		}

		ruleNodes = ruleNodes[:0]
		invalidBody := false
		for exprRow, expr := range rule.Body {
			if uint64(len(diagnostics)) >= uint64(limits.MaxDiagnostics) {
				break
			}
			result := lowerExpression(builder, expr, bindings, &scratch, source)
			if result.code.Valid() {
				span := result.span
				if span.Start == span.End {
					span = locationSpan(source, expr.Location)
				}
				diagnostics = append(diagnostics, newDiagnostic(result.code, span, uint32(exprRow+1), result.field))
				invalidBody = true
				continue
			}
			ruleNodes = append(ruleNodes, result.node)
		}
		if invalidBody || len(ruleNodes) == 0 {
			if !invalidBody && uint64(len(diagnostics)) < uint64(limits.MaxDiagnostics) {
				diagnostics = append(diagnostics, newDiagnostic(public.CodeInvalidPolicy, span, uint32(ruleRow+1), 0))
			}
			continue
		}
		root := ruleNodes[0]
		if len(ruleNodes) > 1 {
			root, err = builder.AddAll(ruleNodes, span)
			if err != nil {
				diagnostics = append(diagnostics, newDiagnostic(builderErrorCode(err), span, uint32(ruleRow+1), 0))
				continue
			}
		}
		roots = append(roots, root)
		rootSpans = append(rootSpans, span)
	}

	if len(diagnostics) != 0 {
		public.SortDiagnostics(diagnostics)
		return nil, diagnostics
	}

	defaultDecision := public.DefaultEscalate
	if defaultRule != nil && !defaultValue {
		defaultDecision = public.DefaultReject
	}
	var root public.NodeID
	switch {
	case defaultRule != nil && defaultValue:
		builder, err = public.NewBuilder(source, bindings, limits)
		if err != nil {
			return nil, []public.Diagnostic{newDiagnostic(builderErrorCode(err), public.Span{}, 0, 0)}
		}
		root, err = builder.AddBoolean(true, locationSpan(source, defaultRule.Location))
	case len(roots) == 0 && defaultRule != nil:
		root, err = builder.AddBoolean(false, locationSpan(source, defaultRule.Location))
	case len(roots) == 1:
		root = roots[0]
	case len(roots) > 1:
		span := public.Span{Start: rootSpans[0].Start, End: rootSpans[len(rootSpans)-1].End}
		root, err = builder.AddAny(roots, span)
	default:
		return nil, []public.Diagnostic{newDiagnostic(public.CodeInvalidPolicy, public.Span{}, 0, 0)}
	}
	if err != nil {
		return nil, []public.Diagnostic{newDiagnostic(builderErrorCode(err), public.Span{}, 0, 0)}
	}
	policy, err := builder.Finish(root, defaultDecision)
	if err != nil {
		return nil, []public.Diagnostic{newDiagnostic(builderErrorCode(err), public.Span{}, 0, 0)}
	}
	return policy, nil
}

// Compile parses and lowers one bounded Rego v1 policy.
func Compile(source []byte, bindings public.BindingSet, limits public.Limits) (*public.Policy, []public.Diagnostic) {
	parsed, diagnostics := Parse(source, bindings, limits)
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}
	return Lower(source, parsed, bindings, limits)
}

func matchingParseInputs(source []byte, parsed *Parsed, bindings public.BindingSet, limits public.Limits) bool {
	return parsed != nil && parsed.module != nil && limits.Valid() && limits == parsed.limits &&
		bytes.Equal(source, parsed.source) && equalBindings(bindings, parsed.bindings)
}

func validateRuleHead(rule *opaast.Rule, decision string) public.DiagnosticCode {
	if rule == nil || rule.Head == nil {
		return public.CodeInvalidPolicy
	}
	head := rule.Head
	if string(head.Name) != decision {
		return public.CodeUnsupported
	}
	if rule.Else != nil || len(head.Args) != 0 || head.Key != nil || len(head.Reference) != 1 {
		return public.CodeUnsupported
	}
	name, ok := head.Reference[0].Value.(opaast.Var)
	if !ok || string(name) != decision {
		return public.CodeUnsupported
	}
	value, ok := head.Value.Value.(opaast.Boolean)
	if !ok {
		return public.CodeType
	}
	if !rule.Default && !bool(value) {
		return public.CodeType
	}
	return public.CodeInvalid
}

func lowerExpression(builder *public.Builder, expr *opaast.Expr, bindings public.BindingSet, scratch *lowerScratch, source []byte) lowerResult {
	if expr == nil || len(expr.With) != 0 {
		return failedLower(public.CodeUnsupported, 0, public.Span{})
	}
	base := lowerAtom(builder, expr, bindings, scratch, source)
	if base.code.Valid() || !expr.Negated {
		return base
	}
	span := locationSpan(source, expr.Location)
	if base.field == 0 || !base.succeedsWhenUndefined {
		node, err := builder.AddNot(base.node, span)
		return completedLower(node, err, 0)
	}
	defined, err := builder.AddDefined(base.field, span)
	if err != nil {
		return completedLower(0, err, base.field)
	}
	notDefined, err := builder.AddNot(defined, span)
	if err != nil {
		return completedLower(0, err, base.field)
	}
	notBase, err := builder.AddNot(base.node, span)
	if err != nil {
		return completedLower(0, err, base.field)
	}
	scratch.children[0], scratch.children[1] = notDefined, notBase
	node, err := builder.AddAny(scratch.children, span)
	return completedLower(node, err, base.field)
}

func lowerAtom(builder *public.Builder, expr *opaast.Expr, bindings public.BindingSet, scratch *lowerScratch, source []byte) lowerResult {
	span := locationSpan(source, expr.Location)
	switch terms := expr.Terms.(type) {
	case *opaast.Term:
		switch value := terms.Value.(type) {
		case opaast.Boolean:
			node, err := builder.AddBoolean(bool(value), span)
			return completedLower(node, err, 0)
		case opaast.Ref:
			field, kind, inputRef, static, found := boundField(value, bindings)
			if !inputRef || !static {
				return failedLower(public.CodeUnsupported, 0, span)
			}
			if !found {
				return failedLower(public.CodeUnknownField, 0, span)
			}
			if kind != public.ValueKindBoolean {
				return failedLower(public.CodeType, field, span)
			}
			node, err := builder.AddCompare(field, public.CompareOpEqual, public.BooleanLiteral(true), span)
			result := completedLower(node, err, field)
			result.succeedsWhenUndefined = true
			return result
		default:
			return failedLower(public.CodeUnsupported, 0, span)
		}
	case []*opaast.Term:
		operator := expr.Operator().String()
		operands := expr.Operands()
		if operator == "internal.member_2" {
			return lowerMembership(builder, operands, span, bindings, scratch)
		}
		return lowerComparison(builder, operator, operands, span, bindings, source)
	default:
		return failedLower(public.CodeUnsupported, 0, span)
	}
}

func lowerComparison(builder *public.Builder, operator string, operands []*opaast.Term, span public.Span, bindings public.BindingSet, source []byte) lowerResult {
	operation := compareOperation(operator)
	if !operation.Valid() || len(operands) != 2 {
		return failedLower(public.CodeUnsupported, 0, span)
	}
	leftField, _, leftInput, leftStatic, leftFound := termField(operands[0], bindings)
	rightField, _, rightInput, rightStatic, rightFound := termField(operands[1], bindings)
	if (leftInput && !leftStatic) || (rightInput && !rightStatic) {
		return failedLower(public.CodeUnsupported, 0, span)
	}
	if leftInput && !leftFound {
		return failedLower(public.CodeUnknownField, 0, locationSpan(source, operands[0].Location))
	}
	if rightInput && !rightFound {
		return failedLower(public.CodeUnknownField, 0, locationSpan(source, operands[1].Location))
	}
	if leftFound && rightFound {
		return failedLower(public.CodeUnsupported, leftField, span)
	}
	if leftFound {
		literal, ok := scalarLiteral(operands[1])
		if !ok {
			return failedLower(public.CodeUnsupported, leftField, span)
		}
		node, err := builder.AddCompare(leftField, operation, literal, span)
		result := completedLower(node, err, leftField)
		result.succeedsWhenUndefined = operation == public.CompareOpEqual
		return result
	}
	if rightFound {
		literal, ok := scalarLiteral(operands[0])
		if !ok {
			return failedLower(public.CodeUnsupported, rightField, span)
		}
		node, err := builder.AddCompare(rightField, reverseOperation(operation), literal, span)
		result := completedLower(node, err, rightField)
		result.succeedsWhenUndefined = operation == public.CompareOpEqual
		return result
	}
	return failedLower(public.CodeUnsupported, 0, span)
}

func lowerMembership(builder *public.Builder, operands []*opaast.Term, span public.Span, bindings public.BindingSet, scratch *lowerScratch) lowerResult {
	if len(operands) != 2 {
		return failedLower(public.CodeUnsupported, 0, span)
	}
	field, _, inputRef, static, found := termField(operands[0], bindings)
	if !inputRef || !static {
		return failedLower(public.CodeUnsupported, 0, span)
	}
	if !found {
		return failedLower(public.CodeUnknownField, 0, span)
	}
	var terms []*opaast.Term
	switch collection := operands[1].Value.(type) {
	case *opaast.Array:
		if collection.Len() > math.MaxUint16 {
			return failedLower(public.CodeLimit, field, span)
		}
		terms = make([]*opaast.Term, collection.Len())
		for row := range terms {
			terms[row] = collection.Elem(row)
		}
	case opaast.Set:
		if collection.Len() > math.MaxUint16 {
			return failedLower(public.CodeLimit, field, span)
		}
		terms = collection.Slice()
	default:
		return failedLower(public.CodeUnsupported, field, span)
	}
	if len(terms) == 0 {
		return failedLower(public.CodeUnsupported, field, span)
	}
	if cap(scratch.literals) < len(terms) {
		scratch.literals = make([]public.Literal, len(terms))
	} else {
		scratch.literals = scratch.literals[:len(terms)]
	}
	for row, term := range terms {
		literal, ok := scalarLiteral(term)
		if !ok {
			return failedLower(public.CodeUnsupported, field, span)
		}
		scratch.literals[row] = literal
	}
	node, err := builder.AddIn(field, scratch.literals, span)
	return completedLower(node, err, field)
}

func termField(term *opaast.Term, bindings public.BindingSet) (public.FieldID, public.ValueKind, bool, bool, bool) {
	if term == nil {
		return 0, public.ValueKindInvalid, false, false, false
	}
	ref, ok := term.Value.(opaast.Ref)
	if !ok {
		return 0, public.ValueKindInvalid, false, false, false
	}
	return boundField(ref, bindings)
}

func boundField(ref opaast.Ref, bindings public.BindingSet) (public.FieldID, public.ValueKind, bool, bool, bool) {
	if len(ref) == 0 {
		return 0, public.ValueKindInvalid, false, false, false
	}
	root, ok := ref[0].Value.(opaast.Var)
	if !ok || root != opaast.Var("input") {
		return 0, public.ValueKindInvalid, false, false, false
	}
	for _, term := range ref[1:] {
		if _, ok := term.Value.(opaast.String); !ok {
			return 0, public.ValueKindInvalid, true, false, false
		}
	}
	for row := range bindings.Fields {
		if refMatchesPath(ref, bindings.Fields[row].Source) {
			return public.FieldID(row + 1), bindings.Fields[row].Kind, true, true, true
		}
	}
	return 0, public.ValueKindInvalid, true, true, false
}

func refMatchesPath(ref opaast.Ref, path string) bool {
	start := 0
	for row, term := range ref {
		end := strings.IndexByte(path[start:], '.')
		if end < 0 {
			end = len(path) - start
		}
		segment := path[start : start+end]
		var value string
		if row == 0 {
			root, ok := term.Value.(opaast.Var)
			if !ok {
				return false
			}
			value = string(root)
		} else {
			part, ok := term.Value.(opaast.String)
			if !ok {
				return false
			}
			value = string(part)
		}
		if segment != value {
			return false
		}
		start += end
		if start < len(path) {
			start++
		}
	}
	return start == len(path)
}

func scalarLiteral(term *opaast.Term) (public.Literal, bool) {
	if term == nil {
		return public.Literal{}, false
	}
	switch value := term.Value.(type) {
	case opaast.String:
		return public.StringLiteral([]byte(value)), true
	case opaast.Number:
		integer, ok := value.Int64()
		if !ok {
			return public.Literal{}, false
		}
		return public.IntegerLiteral(integer), true
	case opaast.Boolean:
		return public.BooleanLiteral(bool(value)), true
	default:
		return public.Literal{}, false
	}
}

func compareOperation(operator string) public.CompareOp {
	switch operator {
	case "equal":
		return public.CompareOpEqual
	case "neq":
		return public.CompareOpNotEqual
	case "lt":
		return public.CompareOpLess
	case "lte":
		return public.CompareOpLessEqual
	case "gt":
		return public.CompareOpGreater
	case "gte":
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

func completedLower(node public.NodeID, err error, field public.FieldID) lowerResult {
	return lowerResult{node: node, field: field, code: builderErrorCode(err)}
}

func failedLower(code public.DiagnosticCode, field public.FieldID, span public.Span) lowerResult {
	return lowerResult{span: span, field: field, code: code}
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
