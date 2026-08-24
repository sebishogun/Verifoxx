package server

import (
	"fmt"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/service"
	verifoxx "github.com/sebishogun/verifoxx/policies/verifoxx"
)

var serverPolicyLimits = jsonpolicy.Limits{
	MaxSourceBytes:   4 << 20,
	MaxCatalogItems:  1024,
	MaxStringBytes:   1 << 20,
	MaxDepth:         128,
	MaxNodes:         1 << 16,
	MaxValues:        1 << 17,
	MaxArrayItems:    1 << 16,
	MaxSymbolBytes:   4 << 20,
	MaxRequirements:  1024,
	MaxClauses:       1 << 13,
	MaxTemplateBytes: 1 << 20,
	MaxAssumptions:   1024,
	MaxUncertainty:   1024,
}

func compilePolicySource(source []byte) (*program.Program, error) {
	document, fields, symbols, diagnostics, err := decodeAndValidatePolicy(source)
	if err != nil {
		return nil, err
	}
	if len(diagnostics) != 0 {
		return nil, fmt.Errorf("%w: policy diagnostics", service.ErrInvalidPolicy)
	}
	var compiled program.Program
	var lowerer compile.Lowerer
	if err := lowerer.Lower(&compiled, document, fields, symbols); err != nil {
		return nil, fmt.Errorf("%w: lower policy: %v", service.ErrInvalidPolicy, err)
	}
	return &compiled, nil
}

func validatePolicySource(source []byte) ([]compile.Diagnostic, error) {
	_, _, _, diagnostics, err := decodeAndValidatePolicy(source)
	return diagnostics, err
}

func decodeAndValidatePolicy(source []byte) (
	*ast.Document,
	*schema.Schema,
	*schema.Interner,
	[]compile.Diagnostic,
	error,
) {
	fields, symbols, err := verifoxx.NewSchema()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: schema: %v", service.ErrInvalidPolicy, err)
	}
	builder := ast.NewBuilder(ast.Hints{SourceBytes: len(source)})
	var decoder jsonpolicy.Decoder
	if err := decoder.Decode(builder, source, fields, symbols, serverPolicyLimits); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: decode policy: %v", service.ErrInvalidPolicy, err)
	}
	var validator compile.Validator
	diagnostics := validator.Validate(nil, builder.Document(), fields)
	return builder.Document(), fields, symbols, diagnostics, nil
}
