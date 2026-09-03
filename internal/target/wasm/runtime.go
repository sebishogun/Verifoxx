package wasm

import (
	"errors"
	"math"
	"reflect"
	"sync/atomic"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/truth"
)

type evaluationFuelProfile struct {
	instructions         uint64
	evidenceInstructions uint64
	listItems            uint64
	operandEdges         uint64
	requirements         uint64
	requirementClauses   uint64
	maxRemediations      uint64
}

type Runtime struct {
	program     *program.Program
	lastError   string
	encoder     frameEncoder
	resultBytes []byte
	output      result.Batch
	executor    eval.Executor
	input       eval.Batch
	manifest    Manifest
	fuelProfile evaluationFuelProfile
	fuel        uint64
	metadata    Metadata
	cancelled   atomic.Bool
	hasInput    bool
	hasResult   bool
}

func NewRuntime(manifest Manifest) (*Runtime, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	runtime := &Runtime{manifest: manifest}
	runtime.metadata = Metadata{
		Limits:               manifest.Limits,
		RequiredCapabilities: manifest.RequiredCapabilities,
		ABI:                  manifest.ABI, Schema: manifest.Schema, Profile: manifest.Profile,
	}
	return runtime, nil
}

func (runtime *Runtime) Metadata() Metadata {
	if runtime == nil {
		return Metadata{}
	}
	return runtime.metadata
}

func (runtime *Runtime) LoadProgram(artifact []byte) ErrorCode {
	if runtime == nil {
		return ErrorInvalidState
	}
	runtime.program = nil
	runtime.fuelProfile = evaluationFuelProfile{}
	runtime.metadata = Metadata{
		Limits:               runtime.manifest.Limits,
		RequiredCapabilities: runtime.manifest.RequiredCapabilities,
		ABI:                  runtime.manifest.ABI, Schema: runtime.manifest.Schema, Profile: runtime.manifest.Profile,
	}
	runtime.clearEvaluation()
	decoded, metadata, err := DecodeProgram(artifact, runtime.manifest)
	if err != nil {
		if errors.Is(err, ErrIncompatibleVersion) {
			return runtime.fail(ErrorIncompatibleVersion, "artifact manifest mismatch")
		}
		return runtime.fail(ErrorInvalidArtifact, "invalid program artifact")
	}
	runtime.program = decoded
	runtime.fuelProfile = newEvaluationFuelProfile(decoded)
	runtime.metadata = metadata
	runtime.executor = eval.Executor{}
	runtime.clearEvaluation()
	runtime.lastError = ""
	return ErrorNone
}

func (runtime *Runtime) UploadInput(frame []byte) ErrorCode {
	if runtime == nil || runtime.program == nil {
		return runtime.fail(ErrorInvalidState, "program is not loaded")
	}
	runtime.hasInput = false
	runtime.hasResult = false
	if err := DecodeInputFrame(&runtime.input, frame, runtime.manifest.Limits); err != nil {
		return runtime.fail(ErrorInvalidFrame, "invalid input frame")
	}
	runtime.hasInput = true
	runtime.hasResult = false
	runtime.resultBytes = runtime.resultBytes[:0]
	runtime.lastError = ""
	return ErrorNone
}

func (runtime *Runtime) Evaluate() (code ErrorCode) {
	if runtime == nil || runtime.program == nil || !runtime.hasInput {
		return runtime.fail(ErrorInvalidState, "input is not loaded")
	}
	runtime.hasResult = false
	defer func() {
		if recover() != nil {
			runtime.clearEvaluation()
			code = runtime.fail(ErrorInternal, "evaluation trapped")
		}
	}()
	if runtime.cancelled.Load() {
		return runtime.fail(ErrorCancelled, "evaluation cancelled")
	}
	cost, ok := evaluationFuelCost(runtime.fuelProfile, runtime.input)
	if !ok {
		return runtime.fail(ErrorFuelExhausted, "evaluation fuel exhausted")
	}
	if runtime.fuel < cost {
		return runtime.fail(ErrorFuelExhausted, "evaluation fuel exhausted")
	}
	runtime.fuel -= cost
	if err := runtime.executor.Execute(&runtime.output, runtime.program, runtime.input); err != nil {
		return runtime.fail(ErrorInvalidFrame, "input does not match program")
	}
	if !validResultBatch(runtime.output) {
		return runtime.fail(ErrorInternal, "evaluator produced an invalid result")
	}
	encoded, err := runtime.encoder.encode(
		runtime.resultBytes[:0], frameResult, reflect.ValueOf(runtime.output),
		runtime.manifest.Limits.MaxOutputBytes, runtime.manifest.Limits.MaxProgramColumns,
	)
	if err != nil || uint64(len(encoded)) > math.MaxUint32 {
		return runtime.fail(ErrorLimitExceeded, "result exceeds linear-memory limit")
	}
	runtime.resultBytes = encoded
	runtime.hasResult = true
	runtime.lastError = ""
	return ErrorNone
}

func newEvaluationFuelProfile(compiled *program.Program) evaluationFuelProfile {
	profile := evaluationFuelProfile{
		instructions:       uint64(len(compiled.Opcodes)),
		requirements:       uint64(len(compiled.RequirementIDs)),
		requirementClauses: uint64(len(compiled.RequirementClauseIDs)),
	}
	for _, opcode := range compiled.Opcodes {
		if opcode == program.OpcodeEvidence {
			profile.evidenceInstructions++
		}
	}
	for _, count := range compiled.ListCounts {
		profile.listItems += uint64(count)
	}
	for _, count := range compiled.OperandCounts {
		profile.operandEdges += uint64(count)
	}
	for _, count := range compiled.ClauseRemediationCounts {
		profile.maxRemediations = max(profile.maxRemediations, uint64(count))
	}
	for _, count := range compiled.Resolutions.RemediationCounts {
		profile.maxRemediations = max(profile.maxRemediations, uint64(count))
	}
	return profile
}

func evaluationFuelCost(profile evaluationFuelProfile, input eval.Batch) (uint64, bool) {
	rows := uint64(input.Rows)
	workRows := max(rows, uint64(1))
	evidenceRows := uint64(input.Evidence.Len())
	evidenceRefs := uint64(len(input.EvidenceRefs))
	cost := uint64(1)

	for _, term := range [...]uint64{
		uint64(len(input.SymbolValues)), uint64(len(input.IntegerValues)), uint64(len(input.TimestampValues)),
		uint64(len(input.BooleanValues)), uint64(len(input.PresenceMasks)), uint64(len(input.RequestIDs)),
		uint64(len(input.EvidenceOffsets)), evidenceRefs,
	} {
		if !addFuel(&cost, term) {
			return 0, false
		}
	}
	for _, product := range [...][2]uint64{
		{workRows, profile.instructions},
		{workRows, profile.operandEdges},
		{rows, profile.listItems},
		{evidenceRows, 8},
		{evidenceRefs, profile.evidenceInstructions},
		{rows, profile.requirements},
		{rows, profile.requirementClauses},
		{rows + 1, 5},
		{rows, 6},
		{rows, profile.requirements},
		{rows, uint64(truth.ReasonCount) * 4},
		{rows, profile.maxRemediations},
	} {
		if !addFuelProduct(&cost, product[0], product[1]) {
			return 0, false
		}
	}
	if !addFuel(&cost, evidenceRefs) {
		return 0, false
	}
	return cost, true
}

func addFuel(total *uint64, value uint64) bool {
	if value > math.MaxUint64-*total {
		return false
	}
	*total += value
	return true
}

func addFuelProduct(total *uint64, left, right uint64) bool {
	if left != 0 && right > math.MaxUint64/left {
		return false
	}
	return addFuel(total, left*right)
}

func (runtime *Runtime) ResultLength() uint32 {
	if runtime == nil || !runtime.hasResult {
		return 0
	}
	return uint32(len(runtime.resultBytes))
}

func (runtime *Runtime) ReadResult(dst []byte) (int, ErrorCode) {
	if runtime == nil || !runtime.hasResult {
		return 0, runtime.fail(ErrorInvalidState, "result is not available")
	}
	if len(dst) < len(runtime.resultBytes) {
		return 0, runtime.fail(ErrorOutputTooSmall, "result buffer is too small")
	}
	written := copy(dst, runtime.resultBytes)
	runtime.lastError = ""
	return written, ErrorNone
}

func (runtime *Runtime) ReadMetadata(dst []byte) (int, ErrorCode) {
	if runtime == nil {
		return 0, ErrorInvalidState
	}
	if len(dst) < MetadataBytes {
		return 0, runtime.fail(ErrorOutputTooSmall, "metadata buffer is too small")
	}
	written, err := encodeMetadata(dst, runtime.metadata)
	if err != nil {
		return 0, runtime.fail(ErrorInternal, "metadata encoding failed")
	}
	runtime.lastError = ""
	return written, ErrorNone
}

func (runtime *Runtime) Reset() ErrorCode {
	if runtime == nil {
		return ErrorInvalidState
	}
	runtime.clearEvaluation()
	runtime.lastError = ""
	return ErrorNone
}

func (runtime *Runtime) Cancel() {
	if runtime != nil {
		runtime.cancelled.Store(true)
	}
}

func (runtime *Runtime) SetFuel(fuel uint64) ErrorCode {
	if runtime == nil {
		return ErrorInvalidState
	}
	if fuel > runtime.manifest.Limits.MaxFuel {
		return runtime.fail(ErrorLimitExceeded, "fuel exceeds configured limit")
	}
	runtime.fuel = fuel
	runtime.lastError = ""
	return ErrorNone
}

func (runtime *Runtime) LastError() string {
	if runtime == nil {
		return "invalid runtime"
	}
	return runtime.lastError
}

func (runtime *Runtime) RecordTrap() ErrorCode {
	if runtime == nil {
		return ErrorInternal
	}
	runtime.clearEvaluation()
	return runtime.fail(ErrorInternal, "module operation trapped")
}

func (runtime *Runtime) clearEvaluation() {
	runtime.hasInput = false
	runtime.hasResult = false
	runtime.fuel = 0
	runtime.resultBytes = runtime.resultBytes[:0]
	runtime.cancelled.Store(false)
}

func (runtime *Runtime) fail(code ErrorCode, message string) ErrorCode {
	if runtime != nil {
		runtime.lastError = message
	}
	return code
}
