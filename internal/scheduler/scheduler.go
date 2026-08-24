package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
)

var (
	// ErrInvalidScheduler reports an unusable configuration, receiver, or call.
	ErrInvalidScheduler = errors.New("scheduler: invalid scheduler")
	// ErrSchedulerClosed reports work submitted after shutdown began.
	ErrSchedulerClosed = errors.New("scheduler: closed")
)

// DefaultParallelRows is the first complete-evaluator crossover measured with
// the weakest supported multiworker configuration.
const DefaultParallelRows uint32 = 256

// Config fixes all scheduler concurrency and reusable storage at construction.
type Config struct {
	Capacity     Capacity
	Workers      int
	QueueDepth   int
	ParallelRows uint32
}

// Stats is an aggregate snapshot of dispatched scheduler work.
type Stats struct {
	Executions uint64
	Serial     uint64
	Parallel   uint64
}

type batchState struct {
	ctx        context.Context
	program    *program.Program
	ranges     []rowRange
	results    []result.Batch
	errors     []error
	batch      eval.Batch
	done       sync.WaitGroup
	shardCount int
}

type shardJob struct {
	state *batchState
	index int
}

// Scheduler executes batches through a fixed worker and admission budget.
type Scheduler struct {
	arena        *Arena
	available    chan *batchState
	workTokens   chan struct{}
	jobs         chan shardJob
	stopping     chan struct{}
	closed       chan struct{}
	states       []batchState
	workerDone   sync.WaitGroup
	serial       atomic.Uint64
	parallel     atomic.Uint64
	workers      int
	queueDepth   int
	closeOnce    sync.Once
	parallelRows uint32
}

// Stats returns a lock-free aggregate execution snapshot.
func (scheduler *Scheduler) Stats() Stats {
	if scheduler == nil {
		return Stats{}
	}
	serial := scheduler.serial.Load()
	parallel := scheduler.parallel.Load()
	return Stats{Executions: serial + parallel, Serial: serial, Parallel: parallel}
}

// NewScheduler allocates all worker, admission, and shard bookkeeping storage.
func NewScheduler(config Config) (*Scheduler, error) {
	if config.Workers <= 0 || config.QueueDepth <= 0 {
		return nil, ErrInvalidScheduler
	}
	if _, err := validateCapacity(config.Capacity); err != nil {
		return nil, ErrInvalidScheduler
	}
	maxInt := int(^uint(0) >> 1)
	if config.QueueDepth > maxInt/config.Workers {
		return nil, ErrInvalidScheduler
	}
	arena, err := NewArena(config.Workers, config.Capacity)
	if err != nil {
		return nil, ErrInvalidScheduler
	}

	scheduler := &Scheduler{
		arena:        arena,
		available:    make(chan *batchState, config.QueueDepth),
		workTokens:   make(chan struct{}, config.Workers),
		jobs:         make(chan shardJob, config.Workers),
		stopping:     make(chan struct{}),
		closed:       make(chan struct{}),
		states:       make([]batchState, config.QueueDepth),
		workers:      config.Workers,
		queueDepth:   config.QueueDepth,
		parallelRows: config.ParallelRows,
	}
	for range config.Workers {
		scheduler.workTokens <- struct{}{}
	}
	for stateIndex := range scheduler.states {
		state := &scheduler.states[stateIndex]
		state.ranges = make([]rowRange, config.Workers)
		state.results = make([]result.Batch, config.Workers)
		state.errors = make([]error, config.Workers)
		scheduler.available <- state
	}
	scheduler.workerDone.Add(config.Workers)
	for range config.Workers {
		go scheduler.worker()
	}
	return scheduler, nil
}

func (scheduler *Scheduler) valid() bool {
	return scheduler != nil && scheduler.arena != nil && scheduler.arena.valid() &&
		scheduler.workers > 0 && scheduler.queueDepth > 0 &&
		len(scheduler.states) == scheduler.queueDepth &&
		scheduler.available != nil && cap(scheduler.available) == scheduler.queueDepth &&
		scheduler.workTokens != nil && cap(scheduler.workTokens) == scheduler.workers &&
		scheduler.jobs != nil && cap(scheduler.jobs) == scheduler.workers &&
		scheduler.stopping != nil && scheduler.closed != nil
}

// Execute evaluates one batch after bounded admission and merges private shards.
func (scheduler *Scheduler) Execute(
	ctx context.Context,
	dst *result.Batch,
	p *program.Program,
	batch eval.Batch,
) error {
	if !scheduler.valid() || dst == nil || p == nil {
		return ErrInvalidScheduler
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-scheduler.stopping:
		return ErrSchedulerClosed
	default:
	}

	var state *batchState
	select {
	case state = <-scheduler.available:
	case <-ctx.Done():
		return ctx.Err()
	case <-scheduler.stopping:
		return ErrSchedulerClosed
	}
	if err := ctx.Err(); err != nil {
		scheduler.available <- state
		return err
	}
	select {
	case <-scheduler.stopping:
		scheduler.available <- state
		return ErrSchedulerClosed
	default:
	}

	select {
	case <-scheduler.workTokens:
	case <-ctx.Done():
		scheduler.available <- state
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		scheduler.workTokens <- struct{}{}
		scheduler.available <- state
		return err
	}

	desired := scheduler.desiredShards(batch.Rows)
	acquired := 1
reserve:
	for acquired < desired {
		select {
		case <-scheduler.workTokens:
			acquired++
		default:
			break reserve
		}
	}
	ranges := partitionRows(state.ranges, batch.Rows, acquired)
	state.shardCount = len(ranges)
	state.ctx = ctx
	state.program = p
	state.batch = batch
	for shardIndex := range ranges {
		state.errors[shardIndex] = nil
	}
	state.done.Add(len(ranges))

	if len(ranges) == 1 {
		scheduler.serial.Add(1)
		scheduler.runShard(state, 0)
	} else {
		scheduler.parallel.Add(1)
		submitted := 0
		for ; submitted < len(ranges); submitted++ {
			select {
			case scheduler.jobs <- shardJob{state: state, index: submitted}:
			case <-ctx.Done():
				for unsent := submitted; unsent < len(ranges); unsent++ {
					state.errors[unsent] = ctx.Err()
					scheduler.workTokens <- struct{}{}
					state.done.Done()
				}
				submitted = len(ranges)
			}
		}
	}
	state.done.Wait()

	err := ctx.Err()
	if err == nil {
		for shardIndex := range ranges {
			if state.errors[shardIndex] != nil {
				err = state.errors[shardIndex]
				break
			}
		}
	}
	if err == nil {
		err = mergeResults(dst, state.results[:len(ranges)], ranges, batch.Rows)
	}
	state.ctx = nil
	state.program = nil
	state.batch = eval.Batch{}
	scheduler.available <- state
	return err
}

// Prime executes enough sequential batches to warm every fixed worker context
// and admission state before a caller starts measuring steady-state work.
func (scheduler *Scheduler) Prime(
	ctx context.Context,
	dst *result.Batch,
	p *program.Program,
	batch eval.Batch,
) error {
	if !scheduler.valid() {
		return ErrInvalidScheduler
	}
	for range max(scheduler.workers, scheduler.queueDepth) {
		if err := scheduler.Execute(ctx, dst, p, batch); err != nil {
			return err
		}
	}
	return nil
}

func (scheduler *Scheduler) desiredShards(rows uint32) int {
	threshold := scheduler.parallelRows
	if threshold == 0 {
		threshold = DefaultParallelRows
	}
	if rows == 0 || threshold == 0 || rows < threshold {
		return 1
	}
	words := int((uint64(rows) + 63) >> 6)
	if words < scheduler.workers {
		return words
	}
	return scheduler.workers
}

// Close rejects new admissions, drains admitted calls, and joins all workers.
func (scheduler *Scheduler) Close() error {
	return scheduler.CloseContext(context.Background())
}

// CloseContext starts shutdown once and waits for completion or cancellation.
func (scheduler *Scheduler) CloseContext(ctx context.Context) error {
	if !scheduler.valid() {
		return ErrInvalidScheduler
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	scheduler.closeOnce.Do(func() {
		close(scheduler.stopping)
		go scheduler.shutdown()
	})
	select {
	case <-scheduler.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (scheduler *Scheduler) shutdown() {
	for range scheduler.states {
		<-scheduler.available
	}
	close(scheduler.jobs)
	scheduler.workerDone.Wait()
	close(scheduler.closed)
}
