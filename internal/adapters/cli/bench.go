package cli

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/scheduler"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/simdops"
)

const (
	defaultBenchmarkRows       = uint32(4096)
	defaultBenchmarkIterations = uint32(100)
	maxBenchmarkRows           = uint32(65536)
	maxBenchmarkIterations     = uint32(100000)
	maxBenchmarkWorkers        = 256
)

var (
	errInvalidBenchmark  = errors.New("benchmark rows, iterations, or workers outside supported bounds")
	errBenchmarkMismatch = errors.New("scheduled benchmark result differs from direct evaluation")
	errBenchmarkStats    = errors.New("benchmark scheduler recorded inconsistent execution modes")
)

type benchmarkOptions struct {
	rows       uint32
	iterations uint32
	workers    int
}

type benchmarkReport struct {
	executionMode  string
	simdTier       string
	elapsedNS      uint64
	rowsPerSecond  uint64
	allocatedBytes uint64
	allocations    uint64
	rows           uint32
	policyNodes    uint32
	evidence       uint32
	evidenceRefs   uint32
	iterations     uint32
	workers        int
}

func newBenchCommand(deps dependencies) *cobra.Command {
	options := benchmarkOptions{
		rows:       defaultBenchmarkRows,
		iterations: defaultBenchmarkIterations,
		workers:    max(1, min(runtime.GOMAXPROCS(0), 4)),
	}
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Benchmark deterministic offline policy evaluation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !options.valid() {
				return usageError(errInvalidBenchmark)
			}
			report, err := runProductBenchmark(cmd.Context(), options, deps)
			if err != nil {
				return operationalError(err)
			}
			return operationalError(writeComplete(cmd.OutOrStdout(), appendBenchmarkReport(nil, report)))
		},
	}
	cmd.Flags().Uint32Var(&options.rows, "rows", options.rows, "request rows per benchmark iteration")
	cmd.Flags().Uint32Var(&options.iterations, "iterations", options.iterations, "measured scheduler executions")
	cmd.Flags().IntVar(&options.workers, "workers", options.workers, "fixed scheduler workers")
	return cmd
}

func (options benchmarkOptions) valid() bool {
	return options.rows > 0 && options.rows <= maxBenchmarkRows &&
		options.iterations > 0 && options.iterations <= maxBenchmarkIterations &&
		options.workers > 0 && options.workers <= maxBenchmarkWorkers
}

func runProductBenchmark(ctx context.Context, options benchmarkOptions, deps dependencies) (benchmarkReport, error) {
	if ctx == nil {
		return benchmarkReport{}, scheduler.ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return benchmarkReport{}, err
	}
	if !options.valid() {
		return benchmarkReport{}, errInvalidBenchmark
	}

	var pipeline engine
	compiled, err := pipeline.compilePolicy([]byte(deps.policy))
	if err != nil {
		return benchmarkReport{}, err
	}
	base, err := pipeline.decodeBatch(compiled, []byte(deps.requests), []byte(deps.evidence))
	if err != nil {
		return benchmarkReport{}, err
	}
	var builder eval.Builder
	batch, err := repeatBenchmarkBatch(&builder, compiled, base, options.rows)
	if err != nil {
		return benchmarkReport{}, err
	}

	var directExecutor eval.Executor
	var direct result.Batch
	if err := directExecutor.Execute(&direct, compiled, batch); err != nil {
		return benchmarkReport{}, pipelineFailure("benchmark direct evaluation", err)
	}
	evaluationScheduler, err := scheduler.NewScheduler(scheduler.Config{
		Capacity:   scheduler.Capacity{Rows: options.rows},
		Workers:    options.workers,
		QueueDepth: 1,
	})
	if err != nil {
		return benchmarkReport{}, pipelineFailure("benchmark scheduler", err)
	}
	defer func() { _ = evaluationScheduler.Close() }()
	var scheduled result.Batch
	if err := evaluationScheduler.Prime(ctx, &scheduled, compiled, batch); err != nil {
		return benchmarkReport{}, err
	}
	if !equalBenchmarkResults(&scheduled, &direct) {
		return benchmarkReport{}, errBenchmarkMismatch
	}

	beforeStats := evaluationScheduler.Stats()
	var beforeMemory, afterMemory runtime.MemStats
	runtime.ReadMemStats(&beforeMemory)
	started := time.Now()
	for range options.iterations {
		if err := evaluationScheduler.Execute(ctx, &scheduled, compiled, batch); err != nil {
			return benchmarkReport{}, err
		}
	}
	elapsed := time.Since(started)
	runtime.ReadMemStats(&afterMemory)
	afterStats := evaluationScheduler.Stats()
	if !equalBenchmarkResults(&scheduled, &direct) {
		return benchmarkReport{}, errBenchmarkMismatch
	}

	serial := afterStats.Serial - beforeStats.Serial
	parallel := afterStats.Parallel - beforeStats.Parallel
	mode := ""
	if serial == uint64(options.iterations) && parallel == 0 {
		mode = "serial"
	} else if parallel == uint64(options.iterations) && serial == 0 {
		mode = "parallel"
	} else {
		return benchmarkReport{}, errBenchmarkStats
	}
	elapsedNS := uint64(max(elapsed.Nanoseconds(), 1))
	rowsPerSecond := uint64(options.rows) * uint64(options.iterations) * uint64(time.Second) / elapsedNS
	return benchmarkReport{
		executionMode: mode, simdTier: simdops.Runtime().Tier,
		elapsedNS: elapsedNS, rowsPerSecond: rowsPerSecond,
		allocatedBytes: afterMemory.TotalAlloc - beforeMemory.TotalAlloc,
		allocations:    afterMemory.Mallocs - beforeMemory.Mallocs,
		rows:           options.rows, policyNodes: uint32(compiled.InstructionCount()),
		evidence: uint32(batch.Evidence.Len()), evidenceRefs: uint32(len(batch.EvidenceRefs)),
		iterations: options.iterations, workers: options.workers,
	}, nil
}

func repeatBenchmarkBatch(builder *eval.Builder, p *program.Program, source eval.Batch, rows uint32) (eval.Batch, error) {
	if builder == nil || p == nil || source.Rows == 0 || rows == 0 {
		return eval.Batch{}, errInvalidBenchmark
	}
	var referenceCount uint64
	for row := uint32(0); row < rows; row++ {
		start, end, ok := source.EvidenceRange(row % source.Rows)
		if !ok {
			return eval.Batch{}, errInvalidBenchmark
		}
		referenceCount += uint64(end - start)
	}
	if referenceCount > uint64(^uint32(0)) || referenceCount > uint64(^uint(0)>>1) {
		return eval.Batch{}, errInvalidBenchmark
	}
	if err := builder.Begin(p, rows, uint32(source.Evidence.Len()), uint32(referenceCount)); err != nil {
		return eval.Batch{}, fmt.Errorf("benchmark batch: %w", err)
	}
	for evidenceRow := range source.Evidence.IDs {
		record := eval.EvidenceRecord{
			Timestamp: source.Evidence.Timestamps[evidenceRow],
			ID:        source.Evidence.IDs[evidenceRow],
			Kind:      source.Evidence.Kinds[evidenceRow],
			State:     source.Evidence.States[evidenceRow],
			Subject:   source.Evidence.Subjects[evidenceRow],
			Scope:     source.Evidence.Scopes[evidenceRow],
			Reviewer:  source.Evidence.Reviewers[evidenceRow],
			Timing:    source.Evidence.Timings[evidenceRow],
		}
		if err := builder.SetEvidence(uint32(evidenceRow), record); err != nil {
			return eval.Batch{}, fmt.Errorf("benchmark evidence %d: %w", evidenceRow, err)
		}
	}
	offsets := make([]uint32, int(rows)+1)
	refs := make([]uint32, int(referenceCount))
	referenceRow := 0
	for row := uint32(0); row < rows; row++ {
		sourceRow := row % source.Rows
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			return eval.Batch{}, fmt.Errorf("benchmark request %d: %w", row, err)
		}
		for fieldRow, kind := range p.FieldIndex.Kinds {
			field := schema.FieldID(fieldRow + 1)
			if !source.Present(field, sourceRow) {
				continue
			}
			column := p.FieldIndex.Columns[fieldRow]
			valueIndex := int(uint64(column)*uint64(source.Rows) + uint64(sourceRow))
			var err error
			switch kind {
			case schema.ValueKindSymbol:
				err = builder.SetSymbol(row, field, source.SymbolValues[valueIndex])
			case schema.ValueKindInteger:
				err = builder.SetInteger(row, field, source.IntegerValues[valueIndex])
			case schema.ValueKindTimestamp:
				err = builder.SetTimestamp(row, field, source.TimestampValues[valueIndex])
			case schema.ValueKindBoolean:
				err = builder.SetBoolean(row, field, source.Boolean(column, sourceRow))
			case schema.ValueKindPresence:
				err = builder.SetPresent(row, field)
			default:
				return eval.Batch{}, errInvalidBenchmark
			}
			if err != nil {
				return eval.Batch{}, fmt.Errorf("benchmark field %d row %d: %w", field, row, err)
			}
		}
		start, end, _ := source.EvidenceRange(sourceRow)
		referenceRow += copy(refs[referenceRow:], source.EvidenceRefs[start:end])
		offsets[row+1] = uint32(referenceRow)
	}
	if err := builder.SetEvidenceCSR(offsets, refs); err != nil {
		return eval.Batch{}, fmt.Errorf("benchmark evidence CSR: %w", err)
	}
	batch, err := builder.Finish()
	if err != nil {
		return eval.Batch{}, fmt.Errorf("benchmark batch: %w", err)
	}
	return batch, nil
}

func equalBenchmarkResults(left, right *result.Batch) bool {
	return left != nil && right != nil && left.Rows == right.Rows &&
		slices.Equal(left.OutcomeIDs, right.OutcomeIDs) &&
		slices.Equal(left.RequirementOffsets, right.RequirementOffsets) && slices.Equal(left.RequirementIDs, right.RequirementIDs) &&
		slices.Equal(left.DriverOffsets, right.DriverOffsets) && slices.Equal(left.DriverRequirements, right.DriverRequirements) &&
		slices.Equal(left.DriverClauses, right.DriverClauses) && slices.Equal(left.DriverNodes, right.DriverNodes) &&
		slices.Equal(left.DriverReasons, right.DriverReasons) && slices.Equal(left.DriverExplanations, right.DriverExplanations) &&
		slices.Equal(left.EvidenceOffsets, right.EvidenceOffsets) && slices.Equal(left.EvidenceIDs, right.EvidenceIDs) &&
		slices.Equal(left.ReasonOffsets, right.ReasonOffsets) && slices.Equal(left.ReasonIDs, right.ReasonIDs) &&
		slices.Equal(left.ReasonNodes, right.ReasonNodes) && slices.Equal(left.ReasonEvidenceIDs, right.ReasonEvidenceIDs) &&
		slices.Equal(left.ReasonEvidenceStates, right.ReasonEvidenceStates) &&
		slices.Equal(left.RemediationOffsets, right.RemediationOffsets) && slices.Equal(left.RemediationIDs, right.RemediationIDs)
}

func appendBenchmarkReport(dst []byte, report benchmarkReport) []byte {
	dst = append(dst, `{"rows":`...)
	dst = strconv.AppendUint(dst, uint64(report.rows), 10)
	dst = append(dst, `,"policy_nodes":`...)
	dst = strconv.AppendUint(dst, uint64(report.policyNodes), 10)
	dst = append(dst, `,"evidence_records":`...)
	dst = strconv.AppendUint(dst, uint64(report.evidence), 10)
	dst = append(dst, `,"evidence_refs":`...)
	dst = strconv.AppendUint(dst, uint64(report.evidenceRefs), 10)
	dst = append(dst, `,"iterations":`...)
	dst = strconv.AppendUint(dst, uint64(report.iterations), 10)
	dst = append(dst, `,"execution_mode":`...)
	dst = strconv.AppendQuote(dst, report.executionMode)
	dst = append(dst, `,"simd_tier":`...)
	dst = strconv.AppendQuote(dst, report.simdTier)
	dst = append(dst, `,"workers":`...)
	dst = strconv.AppendInt(dst, int64(report.workers), 10)
	dst = append(dst, `,"elapsed_ns":`...)
	dst = strconv.AppendUint(dst, report.elapsedNS, 10)
	dst = append(dst, `,"rows_per_second":`...)
	dst = strconv.AppendUint(dst, report.rowsPerSecond, 10)
	dst = append(dst, `,"allocated_bytes":`...)
	dst = strconv.AppendUint(dst, report.allocatedBytes, 10)
	dst = append(dst, `,"allocations":`...)
	dst = strconv.AppendUint(dst, report.allocations, 10)
	return append(dst, "}\n"...)
}
