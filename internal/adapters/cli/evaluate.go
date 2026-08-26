package cli

import (
	"context"
	"errors"
	"runtime"

	"github.com/spf13/cobra"

	public "github.com/sebishogun/verifoxx/frontend"
	"github.com/sebishogun/verifoxx/internal/adapters/jsonbatch"
	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/adapters/jsonresult"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/scheduler"
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
	scheduler     *scheduler.Scheduler
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
	var formatFlags frontendFlags
	cmd := &cobra.Command{
		Use:   "evaluate",
		Short: "Evaluate a batch of requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			inputs, selection, err := loadFrontendSources(flags, formatFlags, cmd.InOrStdin(), deps, sourceAll)
			if err != nil {
				return classifyCommandError(err)
			}
			var engine engine
			var compiled *program.Program
			if selection.language == public.LanguageNative {
				compiled, err = engine.compilePolicy(inputs.policy)
			} else {
				var diagnostics []public.Diagnostic
				compiled, diagnostics, err = compileFrontend(selection, inputs.policy)
				if len(diagnostics) != 0 {
					return writeFrontendDiagnostics(cmd.OutOrStdout(), diagnostics)
				}
			}
			if err != nil {
				return operationalError(err)
			}
			batch, err := engine.decodeBatch(compiled, inputs.requests, inputs.evidence)
			if err != nil {
				return operationalError(err)
			}
			decisions, err := engine.evaluateScheduled(cmd.Context(), compiled, batch)
			closeErr := engine.closeScheduler()
			if err != nil {
				if closeErr != nil {
					err = errors.Join(err, pipelineFailure("close scheduler", closeErr))
				}
				return operationalError(err)
			}
			if closeErr != nil {
				return operationalError(pipelineFailure("close scheduler", closeErr))
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
	bindFrontendFlags(cmd, &formatFlags)
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

func (e *engine) evaluateScheduled(ctx context.Context, p *program.Program, batch eval.Batch) (*result.Batch, error) {
	if ctx == nil {
		return nil, pipelineFailure("evaluate", scheduler.ErrInvalidContext)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.scheduler == nil {
		workers := cliSchedulerWorkers(batch.Rows, runtime.GOMAXPROCS(0))
		created, err := scheduler.NewScheduler(scheduler.Config{
			Capacity:   scheduler.Capacity{Rows: batch.Rows},
			Workers:    workers,
			QueueDepth: 1,
		})
		if err != nil {
			return nil, pipelineFailure("evaluate", err)
		}
		e.scheduler = created
	}
	if err := e.scheduler.Execute(ctx, &e.results, p, batch); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, pipelineFailure("evaluate", err)
	}
	return &e.results, nil
}

func (e *engine) closeScheduler() error {
	if e.scheduler == nil {
		return nil
	}
	return e.scheduler.Close()
}

func cliSchedulerWorkers(rows uint32, workers int) int {
	if rows < scheduler.DefaultParallelRows {
		return 1
	}
	if workers > 256 {
		workers = 256
	}
	words := int((uint64(rows) + 63) >> 6)
	if workers > words {
		workers = words
	}
	if workers < 1 {
		return 1
	}
	return workers
}
