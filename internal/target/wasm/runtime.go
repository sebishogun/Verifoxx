package wasm

import (
	"math"
	"reflect"
	"sync/atomic"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
)

type Runtime struct {
	program     *program.Program
	lastError   string
	encoder     frameEncoder
	resultBytes []byte
	output      result.Batch
	executor    eval.Executor
	input       eval.Batch
	manifest    Manifest
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
	runtime.metadata = Metadata{
		RequiredCapabilities: runtime.manifest.RequiredCapabilities,
		ABI:                  runtime.manifest.ABI, Schema: runtime.manifest.Schema, Profile: runtime.manifest.Profile,
	}
	runtime.clearEvaluation()
	decoded, metadata, err := DecodeProgram(artifact, runtime.manifest.Limits)
	if err != nil {
		return runtime.fail(ErrorInvalidArtifact, "invalid program artifact")
	}
	if metadata.ABI != runtime.manifest.ABI || metadata.Schema != runtime.manifest.Schema ||
		metadata.Profile != runtime.manifest.Profile ||
		metadata.RequiredCapabilities != runtime.manifest.RequiredCapabilities {
		return runtime.fail(ErrorIncompatibleVersion, "artifact manifest mismatch")
	}
	runtime.program = decoded
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
			code = runtime.fail(ErrorInternal, "evaluation trapped")
		}
	}()
	if runtime.cancelled.Load() {
		return runtime.fail(ErrorCancelled, "evaluation cancelled")
	}
	rows := uint64(runtime.input.Rows)
	instructions := uint64(runtime.program.InstructionCount())
	if rows != 0 && instructions > math.MaxUint64/rows {
		return runtime.fail(ErrorFuelExhausted, "evaluation fuel exhausted")
	}
	cost := rows * instructions
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
