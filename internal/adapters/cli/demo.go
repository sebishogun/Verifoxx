package cli

import (
	"errors"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/simdops"
)

var (
	errInvalidDemoMetadata = errors.New("demo has invalid policy or result metadata")
	errMissingDemoRequests = errors.New("demo requires requests R2 and R3")
)

type demoTimings struct {
	compile   time.Duration
	decode    time.Duration
	evaluate  time.Duration
	render    time.Duration
	revise    time.Duration
	aggregate time.Duration
	total     time.Duration
}

type demoReporter struct {
	output       []byte
	explainer    result.Explainer
	materialized result.Materialized
}

func newDemoCommand(deps dependencies) *cobra.Command {
	var flags sourceFlags
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Run the complete policy engine demonstration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			inputs, err := loadSources(flags, cmd.InOrStdin(), deps, sourceAll)
			if err != nil {
				return classifyCommandError(err)
			}
			output, err := runDemo(inputs, deps.version, simdops.Runtime(), time.Now)
			if err != nil {
				return operationalError(err)
			}
			return operationalError(writeComplete(cmd.OutOrStdout(), output))
		},
	}
	bindSourceFlags(cmd, &flags, sourceAll)
	return cmd
}

func runDemo(inputs sources, engineVersion string, runtime simdops.RuntimeInfo, now func() time.Time) ([]byte, error) {
	if len(engineVersion) == 0 || len(runtime.Tier) == 0 || now == nil {
		return nil, errInvalidDemoMetadata
	}

	started := now()
	var pipeline engine
	compiled, err := pipeline.compilePolicy(inputs.policy)
	if err != nil {
		return nil, err
	}
	compiledAt := now()

	batch, err := pipeline.decodeBatch(compiled, inputs.requests, inputs.evidence)
	if err != nil {
		return nil, err
	}
	decodedAt := now()

	decisions, err := pipeline.evaluate(compiled, batch)
	if err != nil {
		return nil, err
	}
	evaluatedAt := now()

	r2Row, r2OK := findDemoRequest(batch, schema.RequestID(2))
	r3Row, r3OK := findDemoRequest(batch, schema.RequestID(3))
	if !r2OK || !r3OK {
		return nil, pipelineFailure("prepare demo", errMissingDemoRequests)
	}
	if uint64(r2Row) >= uint64(len(decisions.OutcomeIDs)) || uint64(r3Row) >= uint64(len(decisions.OutcomeIDs)) {
		return nil, pipelineFailure("prepare demo", errInvalidDemoMetadata)
	}
	r2Baseline := decisions.OutcomeIDs[r2Row]
	r3Baseline := decisions.OutcomeIDs[r3Row]

	report := demoReporter{output: make([]byte, 0, 2048)}
	if err := report.explainer.Bind(compiled.ExplanationCatalog()); err != nil {
		return nil, pipelineFailure("prepare demo", err)
	}
	if err := report.appendHeader(compiled, engineVersion, runtime); err != nil {
		return nil, pipelineFailure("render demo", err)
	}
	if err := report.appendBaseline(batch, decisions); err != nil {
		return nil, pipelineFailure("render demo", err)
	}
	report.output = append(report.output, "\nSCENARIO SIMULATIONS\n--------------------\n"...)
	baselineRenderedAt := now()

	var selector rowSelector
	var overrideStorage [2]fieldOverride
	r3Bindings := [...]string{"environment.usage=standard"}
	r3Start := baselineRenderedAt
	r3Overrides, err := parseOverrides(overrideStorage[:0], compiled, r3Bindings[:])
	if err != nil {
		return nil, pipelineFailure("prepare R3 simulation", err)
	}
	r3Batch, err := selector.compactWithOverrides(compiled, batch, &pipeline.batchBuilder, r3Row, r3Overrides)
	if err != nil {
		return nil, pipelineFailure("select R3", err)
	}
	r3Decisions, err := pipeline.evaluate(compiled, r3Batch)
	if err != nil {
		return nil, err
	}
	if err := report.appendScenario(compiled, r3Batch, r3Decisions, r3Baseline, "environment.usage=standard"); err != nil {
		return nil, pipelineFailure("render R3 simulation", err)
	}
	r3Done := now()

	r2Bindings := [...]string{"action.type=aggregate_analysis", "action.output=aggregate_counts"}
	r2Start := r3Done
	r2Overrides, err := parseOverrides(overrideStorage[:0], compiled, r2Bindings[:])
	if err != nil {
		return nil, pipelineFailure("prepare R2 simulation", err)
	}
	r2Batch, err := selector.compactWithOverrides(compiled, batch, &pipeline.batchBuilder, r2Row, r2Overrides)
	if err != nil {
		return nil, pipelineFailure("select R2", err)
	}
	r2Decisions, err := pipeline.evaluate(compiled, r2Batch)
	if err != nil {
		return nil, err
	}
	if err := report.appendScenario(compiled, r2Batch, r2Decisions, r2Baseline,
		"action.type=aggregate_analysis, action.output=aggregate_counts"); err != nil {
		return nil, pipelineFailure("render R2 simulation", err)
	}
	r2Done := now()

	report.output = append(report.output, "\nPIPELINE TIMINGS\n----------------\n"...)
	report.appendTimings(demoTimings{
		compile:   compiledAt.Sub(started),
		decode:    decodedAt.Sub(compiledAt),
		evaluate:  evaluatedAt.Sub(decodedAt),
		render:    baselineRenderedAt.Sub(evaluatedAt),
		revise:    r3Done.Sub(r3Start),
		aggregate: r2Done.Sub(r2Start),
		total:     r2Done.Sub(started),
	})
	return report.output, nil
}

func findDemoRequest(batch eval.Batch, requestID schema.RequestID) (uint32, bool) {
	if uint64(len(batch.RequestIDs)) != uint64(batch.Rows) {
		return 0, false
	}
	for row := uint32(0); row < batch.Rows; row++ {
		if batch.RequestIDs[row] == requestID {
			return row, true
		}
	}
	return 0, false
}

func (r *demoReporter) appendHeader(compiled *program.Program, engineVersion string, runtime simdops.RuntimeInfo) error {
	if r == nil || compiled == nil || len(engineVersion) == 0 || len(runtime.Tier) == 0 {
		return errInvalidDemoMetadata
	}
	name, ok := compiled.Symbol(compiled.PolicyName)
	if !ok {
		return errInvalidDemoMetadata
	}
	version, ok := compiled.Symbol(compiled.PolicyVersion)
	if !ok {
		return errInvalidDemoMetadata
	}

	r.output = append(r.output, "NORNRUNE POLICY ENGINE DEMO\n===========================\nPolicy: "...)
	r.output = append(r.output, name...)
	r.output = append(r.output, ' ')
	r.output = append(r.output, version...)
	r.output = append(r.output, "\nSHA-256: "...)
	r.output = appendOutputHash(r.output, compiled.ContentHash)
	r.output = append(r.output, "\nEngine: "...)
	r.output = append(r.output, engineVersion...)
	r.output = append(r.output, "\nSIMD: "...)
	r.output = append(r.output, runtime.Tier...)
	if len(runtime.Description) != 0 {
		r.output = append(r.output, " - "...)
		r.output = append(r.output, runtime.Description...)
	}
	r.output = append(r.output, "\nProgram: "...)
	r.output = strconv.AppendUint(r.output, uint64(len(compiled.Opcodes)), 10)
	r.output = append(r.output, " instructions | "...)
	r.output = strconv.AppendUint(r.output, uint64(len(compiled.RequirementIDs)), 10)
	r.output = append(r.output, " requirements | "...)
	r.output = strconv.AppendUint(r.output, uint64(len(compiled.ClauseAssertionRoots)), 10)
	r.output = append(r.output, " clauses\n\nBASELINE DECISIONS\n------------------\n"...)
	return nil
}

func (r *demoReporter) appendBaseline(batch eval.Batch, decisions *result.Batch) error {
	if r == nil || decisions == nil || decisions.Rows != batch.Rows || uint64(len(batch.RequestIDs)) != uint64(batch.Rows) {
		return errInvalidDemoMetadata
	}
	for row := uint32(0); row < batch.Rows; row++ {
		if err := r.appendDecision(batch.RequestIDs[row], decisions, row); err != nil {
			return err
		}
	}
	return nil
}

func (r *demoReporter) appendDecision(requestID schema.RequestID, decisions *result.Batch, row uint32) error {
	if r == nil || requestID == 0 || r.explainer.Materialize(&r.materialized, decisions, row, requestID) != nil {
		return errInvalidDemoMetadata
	}
	rationale, ok := demoTextRange(r.materialized.Bytes, r.materialized.Rationale)
	if !ok || len(r.materialized.Outcome) == 0 {
		return errInvalidDemoMetadata
	}
	r.output = appendRequestID(r.output, requestID)
	r.output = append(r.output, "  "...)
	r.output = append(r.output, r.materialized.Outcome...)
	r.output = append(r.output, "\n    "...)
	r.output = append(r.output, rationale...)
	r.output = append(r.output, '\n')
	return nil
}

func (r *demoReporter) appendScenario(
	compiled *program.Program,
	batch eval.Batch,
	decisions *result.Batch,
	baseline schema.OutcomeID,
	change string,
) error {
	if r == nil || compiled == nil || decisions == nil || batch.Rows != 1 || len(batch.RequestIDs) != 1 || decisions.Rows != 1 {
		return errInvalidDemoMetadata
	}
	baselineName, ok := demoOutcomeName(compiled, baseline)
	if !ok || r.explainer.Materialize(&r.materialized, decisions, 0, batch.RequestIDs[0]) != nil {
		return errInvalidDemoMetadata
	}
	rationale, ok := demoTextRange(r.materialized.Bytes, r.materialized.Rationale)
	if !ok || len(r.materialized.Outcome) == 0 {
		return errInvalidDemoMetadata
	}
	r.output = appendRequestID(r.output, batch.RequestIDs[0])
	r.output = append(r.output, "  "...)
	r.output = append(r.output, change...)
	r.output = append(r.output, "\n    "...)
	r.output = append(r.output, baselineName...)
	r.output = append(r.output, " -> "...)
	r.output = append(r.output, r.materialized.Outcome...)
	r.output = append(r.output, "\n    "...)
	r.output = append(r.output, rationale...)
	r.output = append(r.output, '\n')
	return nil
}

func demoOutcomeName(compiled *program.Program, id schema.OutcomeID) ([]byte, bool) {
	if compiled == nil {
		return nil, false
	}
	outcome, ok := compiled.Outcomes.Lookup(id)
	if !ok {
		return nil, false
	}
	return compiled.Symbol(outcome.Name)
}

func demoTextRange(text []byte, span result.TextRange) ([]byte, bool) {
	if span.Start > span.End || uint64(span.End) > uint64(len(text)) {
		return nil, false
	}
	return text[int(span.Start):int(span.End)], true
}

func appendRequestID(dst []byte, id schema.RequestID) []byte {
	dst = append(dst, 'R')
	return strconv.AppendUint(dst, uint64(id), 10)
}

func (r *demoReporter) appendTimings(timings demoTimings) {
	r.output = appendDemoDuration(r.output, "Compile: ", timings.compile)
	r.output = appendDemoDuration(r.output, "Decode: ", timings.decode)
	r.output = appendDemoDuration(r.output, "Evaluate: ", timings.evaluate)
	r.output = appendDemoDuration(r.output, "Render baseline: ", timings.render)
	r.output = appendDemoDuration(r.output, "Simulate R3: ", timings.revise)
	r.output = appendDemoDuration(r.output, "Simulate R2: ", timings.aggregate)
	r.output = appendDemoDuration(r.output, "Total: ", timings.total)
}

func appendDemoDuration(dst []byte, label string, duration time.Duration) []byte {
	dst = append(dst, label...)
	dst = strconv.AppendInt(dst, duration.Microseconds(), 10)
	return append(dst, " us\n"...)
}
