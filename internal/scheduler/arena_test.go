package scheduler

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"
)

type observedCancelContext struct {
	done    chan struct{}
	waiting chan struct{}
	once    sync.Once
}

func (ctx *observedCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (ctx *observedCancelContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.done
}

func (ctx *observedCancelContext) Err() error {
	select {
	case <-ctx.done:
		return context.Canceled
	default:
		return nil
	}
}

func (ctx *observedCancelContext) Value(any) any { return nil }

func TestArenaOwnership(t *testing.T) {
	t.Run("construction validates bounds before allocation", func(t *testing.T) {
		for _, count := range []int{-1, 0} {
			if _, err := NewArena(count, Capacity{}); !errors.Is(err, ErrInvalidArena) {
				t.Fatalf("NewArena(%d, Capacity{}) error = %v, want %v", count, err, ErrInvalidArena)
			}
		}
		for _, capacity := range []Capacity{
			{InputBytes: -1},
			{OutputBytes: -1},
		} {
			if _, err := NewArena(1, capacity); !errors.Is(err, ErrInvalidCapacity) {
				t.Fatalf("NewArena(1, %+v) error = %v, want %v", capacity, err, ErrInvalidCapacity)
			}
		}
		if strconv.IntSize == 32 {
			if _, err := NewArena(1, Capacity{Rows: math.MaxUint32}); !errors.Is(err, ErrInvalidCapacity) {
				t.Fatalf("NewArena with overflowing row capacity error = %v, want %v", err, ErrInvalidCapacity)
			}
			maxOffsets := uint32(uint64(math.MaxInt32) / 4)
			if _, err := validateCapacity(Capacity{Rows: maxOffsets - 1}); err != nil {
				t.Fatalf("validateCapacity at 32-bit row boundary error = %v", err)
			}
			if _, err := validateCapacity(Capacity{Rows: maxOffsets}); !errors.Is(err, ErrInvalidCapacity) {
				t.Fatalf("validateCapacity above 32-bit row boundary error = %v, want %v", err, ErrInvalidCapacity)
			}
		}

		arena, err := NewArena(2, Capacity{InputBytes: 3, OutputBytes: 5, Rows: 7})
		if err != nil {
			t.Fatalf("NewArena valid error = %v", err)
		}
		if len(arena.contexts) != 2 || len(arena.available) != 2 || cap(arena.available) != 2 {
			t.Fatalf("arena shape = contexts:%d available:%d/%d, want 2 and 2/2", len(arena.contexts), len(arena.available), cap(arena.available))
		}
		for i := range arena.contexts {
			worker := &arena.contexts[i]
			if cap(worker.input) != 3 || cap(worker.output) != 5 || cap(worker.evidenceOffsets) != 8 {
				t.Fatalf("context %d capacities = input:%d output:%d offsets:%d, want 3/5/8", i, cap(worker.input), cap(worker.output), cap(worker.evidenceOffsets))
			}
			if len(worker.input) != 0 || len(worker.output) != 0 || len(worker.evidenceOffsets) != 0 {
				t.Fatalf("context %d active lengths = input:%d output:%d offsets:%d, want zero", i, len(worker.input), len(worker.output), len(worker.evidenceOffsets))
			}
		}
		if _, err := arena.Borrow(nil); !errors.Is(err, ErrInvalidContext) {
			t.Fatalf("Borrow(nil) error = %v, want %v", err, ErrInvalidContext)
		}
		if len(arena.available) != 2 {
			t.Fatalf("Borrow(nil) consumed a context: available = %d, want 2", len(arena.available))
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := arena.Borrow(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-canceled Borrow error = %v, want %v", err, context.Canceled)
		}
		if len(arena.available) != 2 {
			t.Fatalf("pre-canceled Borrow consumed a context: available = %d, want 2", len(arena.available))
		}

		var nilArena *Arena
		if _, err := nilArena.Borrow(context.Background()); !errors.Is(err, ErrInvalidArena) {
			t.Fatalf("nil Arena.Borrow error = %v, want %v", err, ErrInvalidArena)
		}
		if err := nilArena.Return(Lease{}); !errors.Is(err, ErrInvalidArena) {
			t.Fatalf("nil Arena.Return error = %v, want %v", err, ErrInvalidArena)
		}
		var zero Arena
		canceled, cancel = context.WithCancel(context.Background())
		cancel()
		if _, err := zero.Borrow(canceled); !errors.Is(err, ErrInvalidArena) {
			t.Fatalf("zero Arena.Borrow error = %v, want %v", err, ErrInvalidArena)
		}
		if err := zero.Return(Lease{}); !errors.Is(err, ErrInvalidArena) {
			t.Fatalf("zero Arena.Return error = %v, want %v", err, ErrInvalidArena)
		}
		if err := arena.Return(Lease{}); !errors.Is(err, ErrInvalidLease) {
			t.Fatalf("Return zero lease error = %v, want %v", err, ErrInvalidLease)
		}
	})

	t.Run("lease transfers exclusive reusable ownership", func(t *testing.T) {
		arena, err := NewArena(1, Capacity{InputBytes: 4, OutputBytes: 4, Rows: 2})
		if err != nil {
			t.Fatalf("NewArena error = %v", err)
		}
		lease, err := arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("Borrow error = %v", err)
		}
		if lease.context != &arena.contexts[0] || lease.generation == 0 {
			t.Fatalf("lease identity = (%p, %d), want first context and nonzero generation", lease.context, lease.generation)
		}

		queued := &observedCancelContext{done: make(chan struct{}), waiting: make(chan struct{})}
		borrowed := make(chan error, 1)
		go func() {
			_, err := arena.Borrow(queued)
			borrowed <- err
		}()
		<-queued.waiting
		close(queued.done)
		if err := <-borrowed; !errors.Is(err, context.Canceled) {
			t.Fatalf("queued second Borrow error = %v, want %v", err, context.Canceled)
		}

		decoder, err := lease.Decoder()
		if err != nil || decoder != &lease.context.decoder {
			t.Fatalf("Decoder = (%p, %v), want owned decoder", decoder, err)
		}
		builder, err := lease.Builder()
		if err != nil || builder != &lease.context.builder {
			t.Fatalf("Builder = (%p, %v), want owned builder", builder, err)
		}
		executor, err := lease.Executor()
		if err != nil || executor != &lease.context.executor {
			t.Fatalf("Executor = (%p, %v), want owned executor", executor, err)
		}
		batch, err := lease.Result()
		if err != nil || batch != &lease.context.result {
			t.Fatalf("Result = (%p, %v), want owned result", batch, err)
		}

		if err := lease.SetInput([]byte("abc")); err != nil {
			t.Fatalf("SetInput error = %v", err)
		}
		if err := lease.AppendOutput([]byte("xy")); err != nil {
			t.Fatalf("AppendOutput error = %v", err)
		}
		if err := lease.Grow(Capacity{InputBytes: 8, OutputBytes: 9, Rows: 5}); err != nil {
			t.Fatalf("Grow error = %v", err)
		}
		input, err := lease.InputBytes()
		if err != nil || !bytes.Equal(input, []byte("abc")) {
			t.Fatalf("InputBytes = (%q, %v), want abc", input, err)
		}
		if cap(lease.context.input) < 8 || cap(lease.context.output) < 9 || cap(lease.context.evidenceOffsets) < 6 {
			t.Fatalf("grown capacities = input:%d output:%d offsets:%d, want at least 8/9/6", cap(lease.context.input), cap(lease.context.output), cap(lease.context.evidenceOffsets))
		}
		grownInput, grownOutput, grownOffsets := cap(lease.context.input), cap(lease.context.output), cap(lease.context.evidenceOffsets)
		if err := lease.Grow(Capacity{InputBytes: 1, OutputBytes: 1}); err != nil {
			t.Fatalf("nonshrinking Grow error = %v", err)
		}
		if cap(lease.context.input) != grownInput || cap(lease.context.output) != grownOutput || cap(lease.context.evidenceOffsets) != grownOffsets {
			t.Fatalf("Grow shrank capacities to input:%d output:%d offsets:%d", cap(lease.context.input), cap(lease.context.output), cap(lease.context.evidenceOffsets))
		}
		if err := lease.Grow(Capacity{InputBytes: -1}); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("invalid Grow error = %v, want %v", err, ErrInvalidCapacity)
		}

		output, err := lease.ClaimOutput()
		if err != nil {
			t.Fatalf("ClaimOutput error = %v", err)
		}
		if _, err := lease.ClaimOutput(); !errors.Is(err, ErrOutputClaimed) {
			t.Fatalf("second ClaimOutput error = %v, want %v", err, ErrOutputClaimed)
		}
		if err := lease.AppendOutput([]byte("z")); !errors.Is(err, ErrOutputClaimed) {
			t.Fatalf("AppendOutput with active claim error = %v, want %v", err, ErrOutputClaimed)
		}
		if err := lease.ResetOutput(); !errors.Is(err, ErrOutputClaimed) {
			t.Fatalf("ResetOutput with active claim error = %v, want %v", err, ErrOutputClaimed)
		}
		if err := lease.Grow(Capacity{OutputBytes: grownOutput + 1}); !errors.Is(err, ErrOutputClaimed) {
			t.Fatalf("Grow with active claim error = %v, want %v", err, ErrOutputClaimed)
		}
		encoded, err := output.Bytes()
		if err != nil || !bytes.Equal(encoded, []byte("xy")) {
			t.Fatalf("OutputLease.Bytes = (%q, %v), want xy", encoded, err)
		}
		if err := arena.Return(lease); !errors.Is(err, ErrOutputEscaped) {
			t.Fatalf("Return with claimed output error = %v, want %v", err, ErrOutputEscaped)
		}
		if input, err := lease.InputBytes(); err != nil || !bytes.Equal(input, []byte("abc")) {
			t.Fatalf("escaped-output Return mutated active input = (%q, %v)", input, err)
		}
		if err := output.Release(); err != nil {
			t.Fatalf("OutputLease.Release error = %v", err)
		}
		if _, err := output.Bytes(); !errors.Is(err, ErrOutputNotClaimed) {
			t.Fatalf("released OutputLease.Bytes error = %v, want %v", err, ErrOutputNotClaimed)
		}
		if err := output.Release(); !errors.Is(err, ErrOutputNotClaimed) {
			t.Fatalf("second OutputLease.Release error = %v, want %v", err, ErrOutputNotClaimed)
		}
		reclaimed, err := lease.ClaimOutput()
		if err != nil {
			t.Fatalf("second sequential ClaimOutput error = %v", err)
		}
		if _, err := output.Bytes(); !errors.Is(err, ErrOutputNotClaimed) {
			t.Fatalf("prior OutputLease.Bytes during later claim error = %v, want %v", err, ErrOutputNotClaimed)
		}
		if err := output.Release(); !errors.Is(err, ErrOutputNotClaimed) {
			t.Fatalf("prior OutputLease.Release during later claim error = %v, want %v", err, ErrOutputNotClaimed)
		}
		if err := reclaimed.Release(); err != nil {
			t.Fatalf("second sequential OutputLease.Release error = %v", err)
		}

		copied := lease
		if err := arena.Return(lease); err != nil {
			t.Fatalf("Return error = %v", err)
		}
		if err := arena.Return(copied); !errors.Is(err, ErrDoubleReturn) {
			t.Fatalf("copied lease Return error = %v, want %v", err, ErrDoubleReturn)
		}
		if _, err := lease.InputBytes(); !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("returned lease InputBytes error = %v, want %v", err, ErrLeaseExpired)
		}
		if _, err := lease.Executor(); !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("returned lease Executor error = %v, want %v", err, ErrLeaseExpired)
		}
		if err := lease.Grow(Capacity{InputBytes: grownInput + 1}); !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("returned lease Grow error = %v, want %v", err, ErrLeaseExpired)
		}

		next, err := arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("second generation Borrow error = %v", err)
		}
		if next.context != lease.context || next.generation == lease.generation {
			t.Fatalf("second lease identity = (%p, %d), first = (%p, %d)", next.context, next.generation, lease.context, lease.generation)
		}
		if err := arena.Return(lease); !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("old-generation Return error = %v, want %v", err, ErrLeaseExpired)
		}
		if _, err := output.Bytes(); !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("old-generation OutputLease.Bytes error = %v, want %v", err, ErrLeaseExpired)
		}
		if err := output.Release(); !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("old-generation OutputLease.Release error = %v, want %v", err, ErrLeaseExpired)
		}
		if err := next.ResetOutput(); err != nil {
			t.Fatalf("ResetOutput error = %v", err)
		}
		if err := arena.Return(next); err != nil {
			t.Fatalf("second-generation Return error = %v", err)
		}
	})

	t.Run("return clears active state and retains storage", func(t *testing.T) {
		arena, err := NewArena(1, Capacity{InputBytes: 16, OutputBytes: 24, Rows: 3})
		if err != nil {
			t.Fatalf("NewArena error = %v", err)
		}
		lease, err := arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("Borrow error = %v", err)
		}
		if err := lease.SetInput([]byte("input poison")); err != nil {
			t.Fatalf("SetInput error = %v", err)
		}
		if err := lease.AppendOutput([]byte("output poison")); err != nil {
			t.Fatalf("AppendOutput error = %v", err)
		}
		worker := lease.context
		worker.evidenceOffsets = worker.evidenceOffsets[:cap(worker.evidenceOffsets)]
		for i := range worker.evidenceOffsets {
			worker.evidenceOffsets[i] = uint32(i + 1)
		}
		batch, err := lease.Result()
		if err != nil {
			t.Fatalf("Result error = %v", err)
		}
		if err := batch.Reset(2); err != nil {
			t.Fatalf("result Reset poison error = %v", err)
		}
		batch.OutcomeIDs[0] = 1
		batch.RequirementOffsets[1] = 1
		batch.RequirementIDs = append(batch.RequirementIDs, 1)
		batch.DriverOffsets[1] = 1
		batch.DriverRequirements = append(batch.DriverRequirements, 1)
		batch.DriverClauses = append(batch.DriverClauses, 1)
		batch.DriverNodes = append(batch.DriverNodes, 1)
		batch.DriverReasons = append(batch.DriverReasons, 1)
		batch.DriverExplanations = append(batch.DriverExplanations, 1)
		batch.EvidenceOffsets[1] = 1
		batch.EvidenceIDs = append(batch.EvidenceIDs, 1)
		batch.ReasonOffsets[1] = 1
		batch.ReasonIDs = append(batch.ReasonIDs, 1)
		batch.ReasonNodes = append(batch.ReasonNodes, 1)
		batch.ReasonEvidenceIDs = append(batch.ReasonEvidenceIDs, 1)
		batch.ReasonEvidenceStates = append(batch.ReasonEvidenceStates, 1)
		batch.RemediationOffsets[1] = 1
		batch.RemediationIDs = append(batch.RemediationIDs, 1)

		inputCap := cap(worker.input)
		outputCap := cap(worker.output)
		offsetCap := cap(worker.evidenceOffsets)
		outcomeCap := cap(batch.OutcomeIDs)
		requirementCap := cap(batch.RequirementIDs)
		driverExplanationCap := cap(batch.DriverExplanations)
		reasonNodeCap := cap(batch.ReasonNodes)
		reasonEvidenceIDCap := cap(batch.ReasonEvidenceIDs)
		reasonEvidenceStateCap := cap(batch.ReasonEvidenceStates)
		decoder := &worker.decoder
		builder := &worker.builder
		executor := &worker.executor
		resultBatch := &worker.result
		if err := arena.Return(lease); err != nil {
			t.Fatalf("Return poison error = %v", err)
		}

		reused, err := arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("Borrow reused error = %v", err)
		}
		if reused.context != worker {
			t.Fatalf("reused context = %p, want %p", reused.context, worker)
		}
		if len(worker.input) != 0 || len(worker.output) != 0 || len(worker.evidenceOffsets) != 0 {
			t.Fatalf("reused active lengths = input:%d output:%d offsets:%d, want zero", len(worker.input), len(worker.output), len(worker.evidenceOffsets))
		}
		if cap(worker.input) != inputCap || cap(worker.output) != outputCap || cap(worker.evidenceOffsets) != offsetCap {
			t.Fatalf("reused capacities = input:%d output:%d offsets:%d, want %d/%d/%d", cap(worker.input), cap(worker.output), cap(worker.evidenceOffsets), inputCap, outputCap, offsetCap)
		}
		for i, value := range worker.evidenceOffsets[:cap(worker.evidenceOffsets)] {
			if value != 0 {
				t.Fatalf("reused evidence offset %d = %d, want zero", i, value)
			}
		}
		if &worker.decoder != decoder || &worker.builder != builder || &worker.executor != executor || &worker.result != resultBatch {
			t.Fatal("return replaced a context-owned component")
		}
		batch, err = reused.Result()
		if err != nil {
			t.Fatalf("reused Result error = %v", err)
		}
		if batch.Rows != 0 || len(batch.OutcomeIDs) != 0 || len(batch.RequirementIDs) != 0 ||
			len(batch.DriverRequirements) != 0 || len(batch.DriverClauses) != 0 || len(batch.DriverNodes) != 0 ||
			len(batch.DriverReasons) != 0 || len(batch.DriverExplanations) != 0 || len(batch.EvidenceIDs) != 0 ||
			len(batch.ReasonIDs) != 0 || len(batch.ReasonNodes) != 0 || len(batch.ReasonEvidenceIDs) != 0 ||
			len(batch.ReasonEvidenceStates) != 0 || len(batch.RemediationIDs) != 0 {
			t.Fatalf("reused result retained active data: %+v", batch)
		}
		for name, offsets := range map[string][]uint32{
			"requirements": batch.RequirementOffsets,
			"drivers":      batch.DriverOffsets,
			"evidence":     batch.EvidenceOffsets,
			"reasons":      batch.ReasonOffsets,
			"remediations": batch.RemediationOffsets,
		} {
			if len(offsets) != 1 || offsets[0] != 0 {
				t.Fatalf("reused %s offsets = %v, want [0]", name, offsets)
			}
		}
		if cap(batch.OutcomeIDs) != outcomeCap || cap(batch.RequirementIDs) != requirementCap {
			t.Fatalf("reused result capacities = outcomes:%d requirements:%d, want %d/%d", cap(batch.OutcomeIDs), cap(batch.RequirementIDs), outcomeCap, requirementCap)
		}
		if cap(batch.DriverExplanations) != driverExplanationCap || cap(batch.ReasonNodes) != reasonNodeCap ||
			cap(batch.ReasonEvidenceIDs) != reasonEvidenceIDCap || cap(batch.ReasonEvidenceStates) != reasonEvidenceStateCap {
			t.Fatalf("reused provenance capacities = explanations:%d nodes:%d evidence:%d states:%d, want %d/%d/%d/%d",
				cap(batch.DriverExplanations), cap(batch.ReasonNodes), cap(batch.ReasonEvidenceIDs), cap(batch.ReasonEvidenceStates),
				driverExplanationCap, reasonNodeCap, reasonEvidenceIDCap, reasonEvidenceStateCap)
		}
		if err := arena.Return(reused); err != nil {
			t.Fatalf("Return reused error = %v", err)
		}
	})

	t.Run("borrow retires a failed claim published during return", func(t *testing.T) {
		arena, err := NewArena(1, Capacity{})
		if err != nil {
			t.Fatalf("NewArena error = %v", err)
		}
		lease, err := arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("Borrow error = %v", err)
		}
		worker := lease.context
		if err := arena.Return(lease); err != nil {
			t.Fatalf("Return error = %v", err)
		}
		worker.outputClaim.Store(1)
		reused, err := arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("Borrow with transient failed claim error = %v", err)
		}
		if claim := worker.outputClaim.Load(); claim&1 != 0 {
			t.Fatalf("Borrow retained active claim token %d", claim)
		}
		if err := arena.Return(reused); err != nil {
			t.Fatalf("Return reused error = %v", err)
		}
	})

	t.Run("claim token cannot revive within one generation", func(t *testing.T) {
		arena, err := NewArena(1, Capacity{})
		if err != nil {
			t.Fatalf("NewArena error = %v", err)
		}
		lease, err := arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("Borrow error = %v", err)
		}
		lease.context.outputClaim.Store(math.MaxUint32 - 1)
		output, err := lease.ClaimOutput()
		if !errors.Is(err, ErrOutputClaimed) {
			if err == nil {
				_ = output.Release()
			}
			t.Fatalf("ClaimOutput at token limit error = %v, want %v", err, ErrOutputClaimed)
		}
		if err := arena.Return(lease); err != nil {
			t.Fatalf("Return exhausted token error = %v", err)
		}
		reused, err := arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("Borrow after token reset error = %v", err)
		}
		if token := reused.context.outputClaim.Load(); token != 0 {
			t.Fatalf("reused claim token = %d, want 0", token)
		}
		if output, err = reused.ClaimOutput(); err != nil {
			t.Fatalf("ClaimOutput after generation reset error = %v", err)
		}
		if err := output.Release(); err != nil {
			t.Fatalf("Release after generation reset error = %v", err)
		}
		if err := arena.Return(reused); err != nil {
			t.Fatalf("Return reused error = %v", err)
		}
	})

	t.Run("concurrent copied handles remain bounded", func(t *testing.T) {
		arena, err := NewArena(1, Capacity{})
		if err != nil {
			t.Fatalf("NewArena error = %v", err)
		}
		lease, err := arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("Borrow error = %v", err)
		}
		start := make(chan struct{})
		returns := make(chan error, 2)
		for range 2 {
			go func(copied Lease) {
				<-start
				returns <- arena.Return(copied)
			}(lease)
		}
		close(start)
		first, second := <-returns, <-returns
		if !((first == nil && errors.Is(second, ErrDoubleReturn)) || (second == nil && errors.Is(first, ErrDoubleReturn))) {
			t.Fatalf("concurrent copied Return errors = (%v, %v), want one nil and one %v", first, second, ErrDoubleReturn)
		}
		if len(arena.available) != 1 {
			t.Fatalf("available contexts after concurrent Return = %d, want 1", len(arena.available))
		}

		type claimResult struct {
			output OutputLease
			err    error
		}
		for iteration := range 100 {
			lease, err = arena.Borrow(context.Background())
			if err != nil {
				t.Fatalf("iteration %d Borrow error = %v", iteration, err)
			}
			start = make(chan struct{})
			claims := make(chan claimResult, 1)
			returns = make(chan error, 1)
			go func(copied Lease) {
				<-start
				output, err := copied.ClaimOutput()
				claims <- claimResult{output: output, err: err}
			}(lease)
			go func(copied Lease) {
				<-start
				returns <- arena.Return(copied)
			}(lease)
			close(start)
			claim := <-claims
			returned := <-returns
			switch {
			case claim.err == nil:
				if !errors.Is(returned, ErrOutputEscaped) {
					t.Fatalf("iteration %d Return error = %v after successful claim, want %v", iteration, returned, ErrOutputEscaped)
				}
				if err := claim.output.Release(); err != nil {
					t.Fatalf("iteration %d Release error = %v", iteration, err)
				}
				if err := arena.Return(lease); err != nil {
					t.Fatalf("iteration %d retry Return error = %v", iteration, err)
				}
			case returned == nil:
				if !errors.Is(claim.err, ErrLeaseExpired) {
					t.Fatalf("iteration %d ClaimOutput error = %v after successful Return, want %v", iteration, claim.err, ErrLeaseExpired)
				}
			case errors.Is(returned, ErrOutputEscaped) && errors.Is(claim.err, ErrLeaseExpired):
				if err := arena.Return(lease); err != nil {
					t.Fatalf("iteration %d retry Return after crossed operations error = %v", iteration, err)
				}
			default:
				t.Fatalf("iteration %d ClaimOutput/Return errors = (%v, %v)", iteration, claim.err, returned)
			}
			if len(arena.available) != 1 {
				t.Fatalf("iteration %d available contexts = %d, want 1", iteration, len(arena.available))
			}
		}
	})

	t.Run("warm borrow and return allocate zero", func(t *testing.T) {
		arena, err := NewArena(1, Capacity{})
		if err != nil {
			t.Fatalf("NewArena error = %v", err)
		}
		lease, err := arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("prime Borrow error = %v", err)
		}
		if err := arena.Return(lease); err != nil {
			t.Fatalf("prime Return error = %v", err)
		}

		allocs := testing.AllocsPerRun(1000, func() {
			lease, err := arena.Borrow(context.Background())
			if err != nil {
				panic(err)
			}
			if err := arena.Return(lease); err != nil {
				panic(err)
			}
		})
		if allocs != 0 {
			t.Fatalf("warm Borrow plus Return allocations = %g, want 0", allocs)
		}
	})
}
