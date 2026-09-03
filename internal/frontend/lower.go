// Package frontend validates language-neutral semantic policies and lowers
// them into the native immutable execution Program.
package frontend

import (
	"errors"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/internal/ast"
	corecompile "github.com/sebishogun/nornrune/internal/compile"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

var ErrInvalidCompiler = errors.New("frontend compiler: invalid receiver or destination")

var outcomeNames = [...][]byte{
	[]byte("Approve"), []byte("Reject"), []byte("Revise"), []byte("Escalate"),
}

var outcomePrecedence = [...]uint8{1, 4, 2, 3}
var outcomeTerminal = [...]bool{true, true, false, true}

var (
	decisionRationale   = []byte("compatibility policy expression resolved {outcome}")
	unresolvedRationale = []byte("compatibility policy expression is unresolved: {reason}")
)

const (
	approveOutcome  schema.OutcomeID = 1
	rejectOutcome   schema.OutcomeID = 2
	escalateOutcome schema.OutcomeID = 4
)

// Compiler owns reusable validation, translation, and native-lowering scratch.
// It is not safe for concurrent use. Published Programs never borrow it.
type Compiler struct {
	schemaBuilder    schema.Builder
	diagnostics      []public.Diagnostic
	valueIDs         []schema.ValueID
	nodeIDs          []schema.NodeID
	valueScratch     []schema.ValueID
	nodeScratch      []schema.NodeID
	depths           []uint32
	reachable        []uint8
	stack            []public.NodeID
	sourceFieldSlots []uint32
	targetFieldSlots []uint32
	symbols          schema.Interner
	astBuilder       ast.Builder
	lowerer          corecompile.Lowerer
}

// Compile validates and lowers policy into a new self-contained Program.
// Semantic defects are returned as diagnostics with a nil Program and nil
// infrastructure error.
func Compile(policy *public.Policy) (*program.Program, []public.Diagnostic, error) {
	var compiler Compiler
	var compiled program.Program
	diagnostics, err := compiler.Compile(&compiled, policy)
	if err != nil || len(diagnostics) != 0 {
		return nil, diagnostics, err
	}
	return &compiled, diagnostics, nil
}

// Compile validates and lowers policy atomically into dst. Diagnostics remain
// valid until the next call on compiler. On diagnostics or error, dst is
// unchanged.
func (compiler *Compiler) Compile(dst *program.Program, policy *public.Policy) ([]public.Diagnostic, error) {
	if compiler == nil || dst == nil {
		return nil, ErrInvalidCompiler
	}
	diagnostics := compiler.validate(policy)
	if len(diagnostics) != 0 {
		return diagnostics, nil
	}
	if err := compiler.lower(dst, policy); err != nil {
		return nil, err
	}
	return diagnostics, nil
}

func (compiler *Compiler) lower(dst *program.Program, policy *public.Policy) error {
	compiler.astBuilder.Reset()
	compiler.schemaBuilder.Reset()
	compiler.symbols.Reset()
	if err := compiler.astBuilder.SetSource(policy.Source); err != nil {
		return err
	}

	for row := range policy.FieldKinds {
		target, _ := byteRange(policy.FieldBytes, policy.FieldTargetStarts[row], policy.FieldTargetLengths[row])
		name, err := compiler.symbols.Intern(target)
		if err != nil {
			return err
		}
		if _, err := compiler.schemaBuilder.AddField(name, schemaValueKind(policy.FieldKinds[row]), schemaFieldGroup(policy.FieldGroups[row])); err != nil {
			return err
		}
	}
	fields := compiler.schemaBuilder.Finish()

	compiler.valueIDs = resizeIDs(compiler.valueIDs, len(policy.LiteralKinds))
	for row, kind := range policy.LiteralKinds {
		var (
			id  schema.ValueID
			err error
		)
		ref := policy.LiteralRefs[row]
		switch kind {
		case public.ValueKindString:
			value, _ := byteRange(policy.SymbolBytes, policy.SymbolStarts[ref], policy.SymbolLengths[ref])
			id, err = compiler.astBuilder.AddSymbolValue(value)
		case public.ValueKindInteger:
			id, err = compiler.astBuilder.AddIntegerValue(policy.IntegerValues[ref])
		case public.ValueKindBoolean:
			id, err = compiler.astBuilder.AddBooleanValue(policy.BooleanValues[ref] == 1)
		}
		if err != nil {
			return err
		}
		compiler.valueIDs[row] = id
	}

	name, err := compiler.astBuilder.AddSymbolValue(policy.Name)
	if err != nil {
		return err
	}
	version, err := compiler.astBuilder.AddSymbolValue(policy.Version)
	if err != nil {
		return err
	}
	if err := compiler.astBuilder.SetMetadata(name, version); err != nil {
		return err
	}
	if err := compiler.installSemantics(); err != nil {
		return err
	}

	compiler.nodeIDs = resizeNodes(compiler.nodeIDs, len(policy.NodeKinds))
	for row, kind := range policy.NodeKinds {
		span := ast.SourceSpan{Start: policy.NodeSourceStarts[row], End: policy.NodeSourceEnds[row]}
		var node schema.NodeID
		switch kind {
		case public.NodeKindBoolean:
			literal := compiler.valueIDs[policy.NodeLiterals[row]-1]
			value, _ := compiler.astBuilder.Document().BooleanValue(literal)
			node, err = compiler.astBuilder.AddBoolean(value, span)
		case public.NodeKindDefined:
			node, err = compiler.astBuilder.AddDefined(schema.FieldID(policy.NodeFields[row]), span)
		case public.NodeKindExists:
			node, err = compiler.astBuilder.AddExists(schema.FieldID(policy.NodeFields[row]), span)
		case public.NodeKindCompare:
			if policy.NodeOps[row] == public.CompareOpIn {
				start := policy.NodeListStarts[row]
				count := uint32(policy.NodeListCounts[row])
				compiler.valueScratch = resizeIDs(compiler.valueScratch, int(count))
				for offset := uint32(0); offset < count; offset++ {
					compiler.valueScratch[offset] = compiler.valueIDs[policy.ListLiteralIDs[start+offset]-1]
				}
				node, err = compiler.astBuilder.AddIn(schema.FieldID(policy.NodeFields[row]), compiler.valueScratch, span)
			} else {
				node, err = compiler.astBuilder.AddCompare(
					schema.FieldID(policy.NodeFields[row]), astCompareOp(policy.NodeOps[row]),
					compiler.valueIDs[policy.NodeLiterals[row]-1], span,
				)
			}
		case public.NodeKindAll, public.NodeKindAny:
			start := policy.NodeChildStarts[row]
			count := uint32(policy.NodeChildCounts[row])
			compiler.nodeScratch = resizeNodes(compiler.nodeScratch, int(count))
			for offset := uint32(0); offset < count; offset++ {
				compiler.nodeScratch[offset] = compiler.nodeIDs[policy.ChildNodeIDs[start+offset]-1]
			}
			node, err = compiler.astBuilder.AddGroup(astNodeKind(kind), compiler.nodeScratch, span)
		case public.NodeKindNot:
			child := policy.ChildNodeIDs[policy.NodeChildStarts[row]]
			node, err = compiler.astBuilder.AddNot(compiler.nodeIDs[child-1], span)
		}
		if err != nil {
			return err
		}
		compiler.nodeIDs[row] = node
	}

	root := compiler.nodeIDs[policy.Root-1]
	rootSpan := ast.SourceSpan{Start: policy.NodeSourceStarts[policy.Root-1], End: policy.NodeSourceEnds[policy.Root-1]}
	notRoot, err := compiler.astBuilder.AddNot(root, rootSpan)
	if err != nil {
		return err
	}
	applicability, err := compiler.astBuilder.AddGroup(ast.NodeKindAny, []schema.NodeID{root, notRoot}, rootSpan)
	if err != nil {
		return err
	}
	decision, unresolved, err := compiler.explanations()
	if err != nil {
		return err
	}
	defaultOutcome := escalateOutcome
	if policy.Default == public.DefaultReject {
		defaultOutcome = rejectOutcome
	}
	resolution := ast.Resolution{
		OnSatisfied: approveOutcome, OnFalse: rejectOutcome,
		OnMissing: defaultOutcome, OnStale: defaultOutcome, OnUnclear: defaultOutcome,
		OnUnverifiable: defaultOutcome, OnConflict: defaultOutcome,
		OnSatisfiedExplanation: decision, OnFalseExplanation: decision,
		OnMissingExplanation: unresolved, OnStaleExplanation: unresolved,
		OnUnclearExplanation: unresolved, OnUnverifiableExplanation: unresolved,
		OnConflictExplanation: unresolved,
	}
	clause, err := compiler.astBuilder.AddClause(root, nil, resolution, nil, rootSpan)
	if err != nil {
		return err
	}
	if err := compiler.astBuilder.AddRequirement(1, applicability, []schema.ClauseID{clause}, rootSpan); err != nil {
		return err
	}
	return compiler.lowerer.Lower(dst, compiler.astBuilder.Document(), fields, &compiler.symbols)
}

func (compiler *Compiler) installSemantics() error {
	span := ast.SourceSpan{}
	for row, value := range outcomeNames {
		name, err := compiler.astBuilder.AddSymbolValue(value)
		if err != nil {
			return err
		}
		if _, err := compiler.astBuilder.AddOutcome(name, outcomePrecedence[row], outcomeTerminal[row], span); err != nil {
			return err
		}
	}
	return compiler.astBuilder.SetAssumptions(nil)
}

func (compiler *Compiler) explanations() (schema.ExplanationID, schema.ExplanationID, error) {
	decisionTemplate, err := compiler.astBuilder.AddTemplate(decisionRationale, ast.TemplateContextDecision)
	if err != nil {
		return 0, 0, err
	}
	unresolvedTemplate, err := compiler.astBuilder.AddTemplate(unresolvedRationale, ast.TemplateContextUnresolved)
	if err != nil {
		return 0, 0, err
	}
	decision, err := compiler.astBuilder.AddExplanation(decisionTemplate, nil)
	if err != nil {
		return 0, 0, err
	}
	unresolved, err := compiler.astBuilder.AddExplanation(unresolvedTemplate, nil)
	if err != nil {
		return 0, 0, err
	}
	return decision, unresolved, nil
}

func schemaValueKind(kind public.ValueKind) schema.ValueKind {
	switch kind {
	case public.ValueKindString:
		return schema.ValueKindSymbol
	case public.ValueKindInteger:
		return schema.ValueKindInteger
	case public.ValueKindBoolean:
		return schema.ValueKindBoolean
	default:
		return schema.ValueKindInvalid
	}
}

func schemaFieldGroup(group public.FieldGroup) schema.FieldGroup {
	switch group {
	case public.FieldGroupSubject:
		return schema.FieldGroupSubject
	case public.FieldGroupAction:
		return schema.FieldGroupAction
	case public.FieldGroupResource:
		return schema.FieldGroupResource
	case public.FieldGroupOutput:
		return schema.FieldGroupOutput
	case public.FieldGroupContext:
		return schema.FieldGroupContext
	default:
		return schema.FieldGroupInvalid
	}
}

func astCompareOp(operation public.CompareOp) ast.CompareOp {
	switch operation {
	case public.CompareOpEqual:
		return ast.CompareOpEqual
	case public.CompareOpNotEqual:
		return ast.CompareOpNotEqual
	case public.CompareOpLess:
		return ast.CompareOpLess
	case public.CompareOpLessEqual:
		return ast.CompareOpLessEqual
	case public.CompareOpGreater:
		return ast.CompareOpGreater
	case public.CompareOpGreaterEqual:
		return ast.CompareOpGreaterEqual
	default:
		return ast.CompareOpInvalid
	}
}

func astNodeKind(kind public.NodeKind) ast.NodeKind {
	if kind == public.NodeKindAll {
		return ast.NodeKindAll
	}
	return ast.NodeKindAny
}

func resizeIDs(values []schema.ValueID, length int) []schema.ValueID {
	if cap(values) < length {
		return make([]schema.ValueID, length)
	}
	return values[:length]
}

func resizeNodes(values []schema.NodeID, length int) []schema.NodeID {
	if cap(values) < length {
		return make([]schema.NodeID, length)
	}
	return values[:length]
}
