package scheduler

import (
	"math"
	"sync/atomic"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonbatch"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/result"
)

// Capacity supplies cold allocation hints for one reusable worker context.
type Capacity struct {
	InputBytes  int
	OutputBytes int
	Rows        uint32
}

// Context owns all mutable storage used by one serial service worker. Contexts
// are created in one fixed Arena slice and must not be copied after publication.
type Context struct {
	arena           *Arena
	input           []byte
	output          []byte
	evidenceOffsets []uint32
	result          result.Batch
	executor        eval.Executor
	builder         eval.Builder
	decoder         jsonbatch.Decoder
	generation      atomic.Uint64
	state           atomic.Uint32
	outputClaim     atomic.Uint32
}

// Lease is a generation-stamped exclusive claim on one worker context.
type Lease struct {
	arena      *Arena
	context    *Context
	generation uint64
}

// OutputLease keeps encoded output claimed until a response writer releases it.
type OutputLease struct {
	context    *Context
	generation uint64
	claim      uint32
}

func validateCapacity(capacity Capacity) (int, error) {
	if capacity.InputBytes < 0 || capacity.OutputBytes < 0 {
		return 0, ErrInvalidCapacity
	}
	offsets := uint64(capacity.Rows) + 1
	maxInt := uint64(^uint(0) >> 1)
	if offsets > maxInt || offsets > maxInt/4 {
		return 0, ErrInvalidCapacity
	}
	return int(offsets), nil
}

func (lease Lease) active() (*Context, error) {
	if lease.arena == nil || lease.context == nil || lease.generation == 0 || lease.context.arena != lease.arena {
		return nil, ErrInvalidLease
	}
	worker := lease.context
	if worker.generation.Load() != lease.generation || worker.state.Load() != contextBorrowed {
		return nil, ErrLeaseExpired
	}
	return worker, nil
}

func growExact[T any](values []T, capacity int) []T {
	if cap(values) >= capacity {
		return values
	}
	grown := make([]T, len(values), capacity)
	copy(grown, values)
	return grown
}

// Grow raises context-owned capacity without truncating active bytes.
func (lease Lease) Grow(capacity Capacity) error {
	worker, err := lease.active()
	if err != nil {
		return err
	}
	offsets, err := validateCapacity(capacity)
	if err != nil {
		return err
	}
	if worker.outputClaim.Load()&1 != 0 {
		return ErrOutputClaimed
	}
	worker.input = growExact(worker.input, capacity.InputBytes)
	worker.output = growExact(worker.output, capacity.OutputBytes)
	worker.evidenceOffsets = growExact(worker.evidenceOffsets, offsets)
	return nil
}

// SetInput copies source into the context-owned request buffer.
func (lease Lease) SetInput(source []byte) error {
	worker, err := lease.active()
	if err != nil {
		return err
	}
	worker.input = append(worker.input[:0], source...)
	return nil
}

// InputBytes returns the active context-owned request bytes.
func (lease Lease) InputBytes() ([]byte, error) {
	worker, err := lease.active()
	if err != nil {
		return nil, err
	}
	return worker.input, nil
}

// AppendOutput appends encoded response bytes to context-owned storage.
func (lease Lease) AppendOutput(source []byte) error {
	worker, err := lease.active()
	if err != nil {
		return err
	}
	if worker.outputClaim.Load()&1 != 0 {
		return ErrOutputClaimed
	}
	worker.output = append(worker.output, source...)
	return nil
}

// ResetOutput clears the active response length while retaining capacity.
func (lease Lease) ResetOutput() error {
	worker, err := lease.active()
	if err != nil {
		return err
	}
	if worker.outputClaim.Load()&1 != 0 {
		return ErrOutputClaimed
	}
	worker.output = worker.output[:0]
	return nil
}

// Decoder returns the decoder owned by an active lease.
func (lease Lease) Decoder() (*jsonbatch.Decoder, error) {
	worker, err := lease.active()
	if err != nil {
		return nil, err
	}
	return &worker.decoder, nil
}

// Builder returns the typed batch builder owned by an active lease.
func (lease Lease) Builder() (*eval.Builder, error) {
	worker, err := lease.active()
	if err != nil {
		return nil, err
	}
	return &worker.builder, nil
}

// Executor returns the evaluator owned by an active lease.
func (lease Lease) Executor() (*eval.Executor, error) {
	worker, err := lease.active()
	if err != nil {
		return nil, err
	}
	return &worker.executor, nil
}

// Result returns the reusable result batch owned by an active lease.
func (lease Lease) Result() (*result.Batch, error) {
	worker, err := lease.active()
	if err != nil {
		return nil, err
	}
	return &worker.result, nil
}

// ClaimOutput reserves the active encoded response for one consumer.
func (lease Lease) ClaimOutput() (OutputLease, error) {
	worker, err := lease.active()
	if err != nil {
		return OutputLease{}, err
	}
	var claim uint32
	for {
		current := worker.outputClaim.Load()
		if current&1 != 0 || current == math.MaxUint32-1 {
			return OutputLease{}, ErrOutputClaimed
		}
		claim = current + 1
		if worker.outputClaim.CompareAndSwap(current, claim) {
			break
		}
	}
	if worker.generation.Load() != lease.generation || worker.state.Load() != contextBorrowed {
		worker.outputClaim.CompareAndSwap(claim, claim+1)
		return OutputLease{}, ErrLeaseExpired
	}
	return OutputLease{context: worker, generation: lease.generation, claim: claim}, nil
}

// Bytes returns the claimed response bytes while the originating lease remains active.
func (lease OutputLease) Bytes() ([]byte, error) {
	if lease.context == nil || lease.generation == 0 || lease.claim&1 == 0 || lease.context.arena == nil {
		return nil, ErrInvalidLease
	}
	worker := lease.context
	if worker.generation.Load() != lease.generation || worker.state.Load() != contextBorrowed {
		return nil, ErrLeaseExpired
	}
	if worker.outputClaim.Load() != lease.claim {
		return nil, ErrOutputNotClaimed
	}
	return worker.output, nil
}

// Release ends this response-output claim exactly once.
func (lease OutputLease) Release() error {
	if lease.context == nil || lease.generation == 0 || lease.claim&1 == 0 || lease.context.arena == nil {
		return ErrInvalidLease
	}
	worker := lease.context
	if worker.generation.Load() != lease.generation {
		return ErrLeaseExpired
	}
	if !worker.outputClaim.CompareAndSwap(lease.claim, lease.claim+1) {
		return ErrOutputNotClaimed
	}
	return nil
}

func (worker *Context) reset() {
	worker.outputClaim.Store(0)
	clear(worker.evidenceOffsets)
	worker.evidenceOffsets = worker.evidenceOffsets[:0]
	worker.input = worker.input[:0]
	worker.output = worker.output[:0]
	if err := worker.result.Reset(0); err != nil {
		panic("scheduler: invalid owned result batch")
	}
}
