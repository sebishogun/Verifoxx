package diff

import (
	"fmt"
	"unicode/utf8"

	"github.com/sebishogun/nornrune/internal/adapters/jsonpolicy"
	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/compile"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

// FieldGroup identifies one native request-fact group without exposing internal IDs.
type FieldGroup uint8

const (
	FieldGroupInvalid FieldGroup = iota
	FieldGroupSubject
	FieldGroupAction
	FieldGroupResource
	FieldGroupOutput
	FieldGroupContext
)

func (group FieldGroup) Valid() bool {
	return group >= FieldGroupSubject && group <= FieldGroupContext
}

// FieldSpec is one owned public field declaration.
type FieldSpec struct {
	Name  string
	Kind  FieldKind
	Group FieldGroup
}

// FieldSchema owns the ordered field declarations used to compile both policies.
type FieldSchema struct {
	Fields []FieldSpec
}

// Analyzer owns reusable, independent native compilation workers.
type Analyzer struct {
	comparison  comparisonScratch
	oldAST      ast.Builder
	newAST      ast.Builder
	oldProgram  program.Program
	newProgram  program.Program
	oldDecoder  jsonpolicy.Decoder
	newDecoder  jsonpolicy.Decoder
	oldCompiler compile.Lowerer
	newCompiler compile.Lowerer
}

var diffDecoderLimits = jsonpolicy.Limits{
	MaxSourceBytes:   16 << 20,
	MaxCatalogItems:  1024,
	MaxStringBytes:   1 << 20,
	MaxDepth:         256,
	MaxNodes:         1 << 20,
	MaxValues:        1 << 17,
	MaxArrayItems:    1 << 16,
	MaxSymbolBytes:   4 << 20,
	MaxRequirements:  1024,
	MaxClauses:       1 << 13,
	MaxTemplateBytes: 1 << 20,
	MaxAssumptions:   1024,
	MaxUncertainty:   1024,
}

func (a *Analyzer) compilePair(oldSource, newSource []byte, fields FieldSchema) (*program.Program, *program.Program, error) {
	if a == nil {
		return nil, nil, ErrInvalidPolicy
	}
	oldFields, oldSymbols, err := buildFieldSchema(fields)
	if err != nil {
		return nil, nil, err
	}
	newFields, newSymbols, err := buildFieldSchema(fields)
	if err != nil {
		return nil, nil, err
	}
	if err := compileNative(&a.oldDecoder, &a.oldAST, &a.oldCompiler, &a.oldProgram, oldSource, oldFields, oldSymbols); err != nil {
		return nil, nil, err
	}
	if err := compileNative(&a.newDecoder, &a.newAST, &a.newCompiler, &a.newProgram, newSource, newFields, newSymbols); err != nil {
		return nil, nil, err
	}
	if !standardOutcomes(&a.oldProgram) || !standardOutcomes(&a.newProgram) {
		return nil, nil, ErrUnsupportedOutcomes
	}
	return &a.oldProgram, &a.newProgram, nil
}

func buildFieldSchema(fields FieldSchema) (*schema.Schema, *schema.Interner, error) {
	if len(fields.Fields) == 0 {
		return nil, nil, ErrInvalidFieldSchema
	}
	symbols := schema.NewSymbolInterner(len(fields.Fields) * 2)
	builder := schema.NewBuilder()
	for row := range fields.Fields {
		field := fields.Fields[row]
		if field.Name == "" || !utf8.ValidString(field.Name) || !field.Kind.Valid() || !field.Group.Valid() {
			return nil, nil, ErrInvalidFieldSchema
		}
		name, err := symbols.Intern([]byte(field.Name))
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidFieldSchema, err)
		}
		if _, err := builder.AddField(name, schema.ValueKind(field.Kind), schema.FieldGroup(field.Group)); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidFieldSchema, err)
		}
	}
	return builder.Finish(), symbols, nil
}

func compileNative(decoder *jsonpolicy.Decoder, builder *ast.Builder, lowerer *compile.Lowerer, dst *program.Program, source []byte, fields *schema.Schema, symbols *schema.Interner) error {
	if err := decoder.Decode(builder, source, fields, symbols, diffDecoderLimits); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrInvalidPolicy, err)
	}
	if err := lowerer.Lower(dst, builder.Document(), fields, symbols); err != nil {
		return fmt.Errorf("%w: lower: %v", ErrInvalidPolicy, err)
	}
	return nil
}

func standardOutcomes(compiled *program.Program) bool {
	if compiled == nil || len(compiled.Outcomes.Names) != len(decisionNames) {
		return false
	}
	for row, nameID := range compiled.Outcomes.Names {
		name, ok := compiled.Symbol(nameID)
		if !ok || string(name) != decisionNames[row] {
			return false
		}
	}
	return true
}
