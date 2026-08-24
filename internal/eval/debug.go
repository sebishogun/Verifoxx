package eval

import (
	"errors"
	"math"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

// ErrInvalidRetainedExecution reports an invalid debug lifecycle operation.
var ErrInvalidRetainedExecution = errors.New("eval: invalid retained execution")

// RetainedExecutor evaluates one immutable batch in deterministic scalar
// instruction order. Its private Program copy replaces only scratch slot
// columns so every instruction result remains available for the session.
type RetainedExecutor struct {
	truthSlots   []schema.SlotID
	reasonSlots  []schema.SlotID
	result       result.Batch
	executor     Executor
	batch        Batch
	program      program.Program
	next         uint32
	active       bool
	complete     bool
	usesEvidence bool
}

// Begin validates and prepares one retained execution without running the
// schedule. Program and batch storage remain borrowed and must stay immutable.
func (retained *RetainedExecutor) Begin(source *program.Program, batch Batch) error {
	if retained == nil || source == nil || len(source.Opcodes) == 0 || uint64(len(source.Opcodes)) > math.MaxUint32 {
		return ErrInvalidRetainedExecution
	}
	instructions := len(source.Opcodes)
	retained.truthSlots = resizeExecutorScratch(retained.truthSlots, instructions)
	retained.reasonSlots = resizeExecutorScratch(retained.reasonSlots, instructions)
	for row := range instructions {
		slot := schema.SlotID(row + 1)
		retained.truthSlots[row] = slot
		retained.reasonSlots[row] = slot
	}
	retained.program = *source
	retained.program.TruthSlots = retained.truthSlots
	retained.program.ReasonSlots = retained.reasonSlots
	retained.program.TruthSlotCount = uint32(instructions)
	retained.program.ReasonSlotCount = uint32(instructions)
	retained.executor.program = nil
	usesEvidence, err := retained.executor.prepareExecution(
		&retained.result, &retained.program, batch, executionScalar,
	)
	if err != nil {
		retained.active = false
		retained.complete = false
		return err
	}
	retained.batch = batch
	retained.next = 0
	retained.active = true
	retained.complete = false
	retained.usesEvidence = usesEvidence
	return nil
}

// Step executes one instruction and finalizes results after the final row.
func (retained *RetainedExecutor) Step() (schema.InstructionID, bool, error) {
	if retained == nil || !retained.active || retained.complete ||
		uint64(retained.next) >= uint64(len(retained.program.Opcodes)) {
		return 0, retained != nil && retained.complete, ErrInvalidRetainedExecution
	}
	row := int(retained.next)
	retained.executor.executeInstructionMode(&retained.program, retained.batch, row, executionScalar)
	retained.next++
	if uint64(retained.next) == uint64(len(retained.program.Opcodes)) {
		retained.executor.finalizeResults(
			&retained.result, &retained.program, retained.batch, retained.usesEvidence,
		)
		retained.complete = true
	}
	return schema.InstructionID(row + 1), retained.complete, nil
}

// Rewind moves to an already executed instruction boundary. Retain-all slots
// preserve every operand before cursor; later slots are overwritten in order.
func (retained *RetainedExecutor) Rewind(cursor uint32) error {
	if retained == nil || !retained.active || cursor > retained.next ||
		uint64(cursor) > uint64(len(retained.program.Opcodes)) {
		return ErrInvalidRetainedExecution
	}
	if err := retained.result.Reset(retained.batch.Rows); err != nil {
		return ErrBatchTooLarge
	}
	retained.next = cursor
	retained.complete = false
	if uint64(cursor) == uint64(len(retained.program.Opcodes)) {
		retained.executor.finalizeResults(
			&retained.result, &retained.program, retained.batch, retained.usesEvidence,
		)
		retained.complete = true
	}
	return nil
}

// Restart rewinds to the boundary before instruction one.
func (retained *RetainedExecutor) Restart() error {
	return retained.Rewind(0)
}

// CopyInstruction copies one executed instruction's retained mask planes into
// exact-size caller storage.
func (retained *RetainedExecutor) CopyInstruction(
	instruction schema.InstructionID,
	positive, negative, reasons []uint64,
) error {
	words := truth.WordCount(retained.Rows())
	if retained == nil || !retained.active || instruction == 0 || uint32(instruction) > retained.next ||
		len(positive) != words || len(negative) != words || len(reasons) != truth.ReasonCount*words {
		return ErrInvalidRetainedExecution
	}
	row := instruction - 1
	planes := retained.executor.truthSlot(retained.program.TruthSlots[row], retained.batch.Rows)
	reasonPlanes := retained.executor.reasonSlot(retained.program.ReasonSlots[row], retained.batch.Rows)
	copy(positive, planes.Positive)
	copy(negative, planes.Negative)
	copy(reasons, reasonPlanes.Words)
	return nil
}

// InstructionTruth returns one executed row's four-valued truth bits.
func (retained *RetainedExecutor) InstructionTruth(
	instruction schema.InstructionID,
	row uint32,
) (positive, negative, ok bool) {
	if retained == nil || !retained.active || instruction == 0 || uint32(instruction) > retained.next ||
		uint64(instruction) > uint64(len(retained.program.Opcodes)) || row >= retained.batch.Rows {
		return false, false, false
	}
	positive, negative = retained.executor.instructionTruth(&retained.program, instruction, row, retained.batch.Rows)
	return positive, negative, true
}

// InstructionReasons returns one executed row's retained reason mask.
func (retained *RetainedExecutor) InstructionReasons(
	instruction schema.InstructionID,
	row uint32,
) (truth.ReasonMask, bool) {
	if retained == nil || !retained.active || instruction == 0 || uint32(instruction) > retained.next ||
		uint64(instruction) > uint64(len(retained.program.Opcodes)) || row >= retained.batch.Rows {
		return 0, false
	}
	return retained.executor.instructionReasons(&retained.program, instruction, row, retained.batch.Rows), true
}

// Result returns read-only session-owned result storage after completion.
func (retained *RetainedExecutor) Result() (*result.Batch, bool) {
	if retained == nil || !retained.complete {
		return nil, false
	}
	return &retained.result, true
}

// Cursor is the count of instructions executed in the current replay.
func (retained *RetainedExecutor) Cursor() uint32 {
	if retained == nil || !retained.active {
		return 0
	}
	return retained.next
}

// InstructionCount returns the fixed schedule length.
func (retained *RetainedExecutor) InstructionCount() int {
	if retained == nil || !retained.active {
		return 0
	}
	return len(retained.program.Opcodes)
}

// Rows returns the immutable batch width.
func (retained *RetainedExecutor) Rows() uint32 {
	if retained == nil || !retained.active {
		return 0
	}
	return retained.batch.Rows
}

// Complete reports whether the schedule and result reduction have finished.
func (retained *RetainedExecutor) Complete() bool {
	return retained != nil && retained.complete
}
