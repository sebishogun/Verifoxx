package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonbatch"
	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/adapters/jsonresult"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	verifoxx "github.com/sebishogun/verifoxx/policies/verifoxx"
)

var errInvalidPolicy = errors.New("policy validation failed")

var policyLimits = jsonpolicy.Limits{
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

var batchLimits = jsonbatch.Limits{
	MaxRequestBytes:       8 << 20,
	MaxEvidenceBytes:      8 << 20,
	MaxStringBytes:        1 << 20,
	MaxRequests:           1 << 16,
	MaxEvidence:           1 << 18,
	MaxEvidenceRefs:       1 << 20,
	MaxFactsPerRequest:    256,
	MaxEvidenceAttributes: 64,
	MaxDepth:              128,
}

type pipelineError struct {
	err   error
	stage string
}

func (e *pipelineError) Error() string { return e.stage + ": " + e.err.Error() }
func (e *pipelineError) Unwrap() error { return e.err }

func pipelineFailure(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &pipelineError{err: err, stage: stage}
}

type decodedPolicy struct {
	document *ast.Document
	fields   *schema.Schema
	symbols  *schema.Interner
}

type engine struct {
	policyBuilder *ast.Builder
	policyDecoder jsonpolicy.Decoder
	validator     compile.Validator
	diagnostics   []compile.Diagnostic
	results       result.Batch
	executor      eval.Executor
	batchBuilder  eval.Builder
	batchDecoder  jsonbatch.Decoder
	program       program.Program
	lowerer       compile.Lowerer
}

func newEvaluateCommand(deps dependencies) *cobra.Command {
	var flags sourceFlags
	cmd := &cobra.Command{
		Use:   "evaluate",
		Short: "Evaluate a batch of requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			inputs, err := loadSources(flags, cmd.InOrStdin(), deps, sourceAll)
			if err != nil {
				return classifyCommandError(err)
			}
			var engine engine
			compiled, err := engine.compilePolicy(inputs.policy)
			if err != nil {
				return operationalError(err)
			}
			batch, err := engine.decodeBatch(compiled, inputs.requests, inputs.evidence)
			if err != nil {
				return operationalError(err)
			}
			decisions, err := engine.evaluate(compiled, batch)
			if err != nil {
				return operationalError(err)
			}
			var encoder jsonresult.Encoder
			if err := encoder.Bind(compiled); err != nil {
				return operationalError(pipelineFailure("encode results", err))
			}
			encoded, err := encoder.Append(nil, batch.RequestIDs, decisions, []byte(deps.version))
			if err != nil {
				return operationalError(pipelineFailure("encode results", err))
			}
			return operationalError(writeComplete(cmd.OutOrStdout(), encoded))
		},
	}
	bindSourceFlags(cmd, &flags, sourceAll)
	return cmd
}

func (e *engine) decodePolicy(source []byte) (decodedPolicy, error) {
	fields, symbols, err := verifoxx.NewSchema()
	if err != nil {
		return decodedPolicy{}, pipelineFailure("schema", err)
	}
	e.policyBuilder = ast.NewBuilder(ast.Hints{SourceBytes: len(source)})
	if err := e.policyDecoder.Decode(e.policyBuilder, source, fields, symbols, policyLimits); err != nil {
		return decodedPolicy{}, pipelineFailure("decode policy", err)
	}
	return decodedPolicy{document: e.policyBuilder.Document(), fields: fields, symbols: symbols}, nil
}

func (e *engine) validatePolicy(policy decodedPolicy) []compile.Diagnostic {
	e.diagnostics = e.validator.Validate(e.diagnostics[:0], policy.document, policy.fields)
	return e.diagnostics
}

func (e *engine) lowerPolicy(policy decodedPolicy) (*program.Program, error) {
	if len(e.validatePolicy(policy)) != 0 {
		return nil, pipelineFailure("validate policy", errInvalidPolicy)
	}
	if err := e.lowerer.Lower(&e.program, policy.document, policy.fields, policy.symbols); err != nil {
		return nil, pipelineFailure("compile policy", err)
	}
	return &e.program, nil
}

func (e *engine) compilePolicy(source []byte) (*program.Program, error) {
	policy, err := e.decodePolicy(source)
	if err != nil {
		return nil, err
	}
	return e.lowerPolicy(policy)
}

func (e *engine) decodeBatch(p *program.Program, requests, evidence []byte) (eval.Batch, error) {
	batch, err := e.batchDecoder.Decode(&e.batchBuilder, p, requests, evidence, batchLimits)
	if err != nil {
		return eval.Batch{}, pipelineFailure("decode batch", err)
	}
	return batch, nil
}

func (e *engine) evaluate(p *program.Program, batch eval.Batch) (*result.Batch, error) {
	if err := e.executor.Execute(&e.results, p, batch); err != nil {
		return nil, pipelineFailure("evaluate", err)
	}
	return &e.results, nil
}
