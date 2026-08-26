package server

import (
	"fmt"

	"github.com/sebishogun/nornrune/internal/adapters/jsonpolicy"
	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/compile"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/security"
	"github.com/sebishogun/nornrune/internal/service"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func policyDecoderLimits(limits security.Limits) jsonpolicy.Limits {
	return jsonpolicy.Limits{
		MaxSourceBytes:   limits.MaxPolicyBytes,
		MaxCatalogItems:  1024,
		MaxStringBytes:   1 << 20,
		MaxDepth:         limits.MaxASTDepth,
		MaxNodes:         limits.MaxASTNodes,
		MaxValues:        1 << 17,
		MaxArrayItems:    1 << 16,
		MaxSymbolBytes:   4 << 20,
		MaxRequirements:  1024,
		MaxClauses:       1 << 13,
		MaxTemplateBytes: 1 << 20,
		MaxAssumptions:   1024,
		MaxUncertainty:   1024,
	}
}

func compilePolicySource(source []byte) (*program.Program, error) {
	return compilePolicySourceWithLimits(source, security.DefaultLimits())
}

func compilePolicySourceWithLimits(source []byte, limits security.Limits) (*program.Program, error) {
	if limits.Validate() != nil {
		return nil, service.ErrInvalidPolicy
	}
	document, fields, symbols, diagnostics, err := decodeAndValidatePolicy(source, policyDecoderLimits(limits))
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
	return validatePolicySourceWithLimits(source, security.DefaultLimits())
}

func validatePolicySourceWithLimits(source []byte, limits security.Limits) ([]compile.Diagnostic, error) {
	if limits.Validate() != nil {
		return nil, service.ErrInvalidPolicy
	}
	_, _, _, diagnostics, err := decodeAndValidatePolicy(source, policyDecoderLimits(limits))
	return diagnostics, err
}

func decodeAndValidatePolicy(source []byte, limits jsonpolicy.Limits) (
	*ast.Document,
	*schema.Schema,
	*schema.Interner,
	[]compile.Diagnostic,
	error,
) {
	fields, symbols, err := nornrune.NewSchema()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: schema: %v", service.ErrInvalidPolicy, err)
	}
	builder := ast.NewBuilder(ast.Hints{SourceBytes: len(source)})
	var decoder jsonpolicy.Decoder
	if err := decoder.Decode(builder, source, fields, symbols, limits); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: decode policy: %v", service.ErrInvalidPolicy, err)
	}
	var validator compile.Validator
	diagnostics := validator.Validate(nil, builder.Document(), fields)
	return builder.Document(), fields, symbols, diagnostics, nil
}
