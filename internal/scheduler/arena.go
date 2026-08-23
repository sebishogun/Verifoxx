package scheduler

import (
	"context"
	"errors"
)

var (
	// ErrInvalidArena reports a nil arena or a nonpositive context count.
	ErrInvalidArena = errors.New("scheduler: invalid arena")
	// ErrInvalidCapacity reports a negative byte hint or host slice overflow.
	ErrInvalidCapacity = errors.New("scheduler: invalid capacity")
	// ErrInvalidContext reports a nil cancellation context passed to Borrow.
	ErrInvalidContext = errors.New("scheduler: invalid cancellation context")
	// ErrInvalidLease reports an empty lease or one belonging to another arena.
	ErrInvalidLease = errors.New("scheduler: invalid lease")
	// ErrLeaseExpired reports a lease whose context is returned or reused.
	ErrLeaseExpired = errors.New("scheduler: lease expired")
	// ErrDoubleReturn reports a copied lease returned more than once.
	ErrDoubleReturn = errors.New("scheduler: context already returned")
	// ErrOutputClaimed reports a second claim on active response bytes.
	ErrOutputClaimed = errors.New("scheduler: output already claimed")
	// ErrOutputNotClaimed reports use or release of an inactive output claim.
	ErrOutputNotClaimed = errors.New("scheduler: output not claimed")
	// ErrOutputEscaped reports a return attempted while response bytes are claimed.
	ErrOutputEscaped = errors.New("scheduler: output still claimed")
)

const (
	contextAvailable uint32 = iota
	contextBorrowed
	contextReturning
)

// Arena transfers exclusive ownership of a fixed set of reusable contexts.
type Arena struct {
	available chan *Context
	contexts  []Context
}

func (arena *Arena) valid() bool {
	return arena != nil && arena.available != nil && len(arena.contexts) > 0 && cap(arena.available) == len(arena.contexts)
}

// NewArena creates and publishes exactly count reusable worker contexts.
func NewArena(count int, capacity Capacity) (*Arena, error) {
	if count <= 0 {
		return nil, ErrInvalidArena
	}
	offsets, err := validateCapacity(capacity)
	if err != nil {
		return nil, err
	}
	arena := &Arena{
		contexts:  make([]Context, count),
		available: make(chan *Context, count),
	}
	for i := range arena.contexts {
		worker := &arena.contexts[i]
		worker.input = make([]byte, 0, capacity.InputBytes)
		worker.output = make([]byte, 0, capacity.OutputBytes)
		worker.evidenceOffsets = make([]uint32, 0, offsets)
		worker.arena = arena
		worker.state.Store(contextAvailable)
		arena.available <- worker
	}
	return arena, nil
}

// Borrow waits for exclusive context ownership or caller cancellation.
func (arena *Arena) Borrow(ctx context.Context) (Lease, error) {
	if !arena.valid() {
		return Lease{}, ErrInvalidArena
	}
	if ctx == nil {
		return Lease{}, ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	select {
	case worker := <-arena.available:
		if worker == nil || worker.arena != arena {
			panic("scheduler: invalid available context")
		}
		retireFailedClaim(worker)
		generation := worker.generation.Add(1)
		if generation == 0 {
			generation = worker.generation.Add(1)
		}
		if !worker.state.CompareAndSwap(contextAvailable, contextBorrowed) {
			panic("scheduler: invalid context ownership state")
		}
		return Lease{arena: arena, context: worker, generation: generation}, nil
	case <-ctx.Done():
		return Lease{}, ctx.Err()
	}
}

func retireFailedClaim(worker *Context) {
	for {
		claim := worker.outputClaim.Load()
		if claim&1 == 0 || worker.outputClaim.CompareAndSwap(claim, claim+1) {
			return
		}
	}
}

// Return resets and republishes one active lease exactly once.
func (arena *Arena) Return(lease Lease) error {
	if !arena.valid() {
		return ErrInvalidArena
	}
	if lease.arena == nil || lease.context == nil || lease.generation == 0 || lease.arena != arena || lease.context.arena != arena {
		return ErrInvalidLease
	}
	worker := lease.context
	if worker.generation.Load() != lease.generation {
		return ErrLeaseExpired
	}
	if !worker.state.CompareAndSwap(contextBorrowed, contextReturning) {
		if worker.generation.Load() != lease.generation {
			return ErrLeaseExpired
		}
		return ErrDoubleReturn
	}
	if worker.outputClaim.Load()&1 != 0 {
		worker.state.Store(contextBorrowed)
		return ErrOutputEscaped
	}
	worker.reset()
	worker.state.Store(contextAvailable)
	select {
	case arena.available <- worker:
		return nil
	default:
		panic("scheduler: duplicate context publication")
	}
}
