package postgres

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sebishogun/nornrune/internal/persistence"
)

// JournalConfig fixes all writer concurrency and slot storage at construction.
// QueueDepth is the total admitted budget, including active writes.
type JournalConfig struct {
	Capacity     persistence.AuditCapacity
	WriteTimeout time.Duration
	Mode         persistence.AuditMode
	Writers      int
	QueueDepth   int
}

// JournalStats is one lock-free batch-level counter snapshot.
type JournalStats struct {
	Accepted  uint64
	Succeeded uint64
	Failed    uint64
	Dropped   uint64
	InFlight  uint64
}

type journalSlot struct {
	ctx      context.Context
	ack      chan error
	batch    persistence.AuditBatch
	required bool
}

// Journal owns fixed audit batches and PostgreSQL writer goroutines.
type Journal struct {
	store        persistence.AuditStore
	available    chan *journalSlot
	jobs         chan *journalSlot
	stopping     chan struct{}
	closed       chan struct{}
	slots        []journalSlot
	workersDone  sync.WaitGroup
	accepted     atomic.Uint64
	succeeded    atomic.Uint64
	failed       atomic.Uint64
	dropped      atomic.Uint64
	inFlight     atomic.Uint64
	writeTimeout time.Duration
	closeOnce    sync.Once
	mode         persistence.AuditMode
}

// NewJournal allocates all persistent-mode storage before starting writers.
// Off mode has no store, allocation, or goroutine dependency.
func NewJournal(store persistence.AuditStore, config JournalConfig) (*Journal, error) {
	if !config.Mode.Valid() {
		return nil, fmt.Errorf("%w: audit mode", persistence.ErrInvalidJournal)
	}
	journal := &Journal{
		store:        store,
		stopping:     make(chan struct{}),
		closed:       make(chan struct{}),
		writeTimeout: config.WriteTimeout,
		mode:         config.Mode,
	}
	if config.Mode == persistence.AuditOff {
		return journal, nil
	}
	if store == nil || config.Writers <= 0 || config.QueueDepth <= 0 ||
		config.Writers > config.QueueDepth || config.WriteTimeout <= 0 {
		return nil, fmt.Errorf("%w: writer configuration", persistence.ErrInvalidJournal)
	}

	journal.available = make(chan *journalSlot, config.QueueDepth)
	journal.jobs = make(chan *journalSlot, config.QueueDepth)
	journal.slots = make([]journalSlot, config.QueueDepth)
	for index := range journal.slots {
		batch, err := persistence.NewAuditBatch(config.Capacity)
		if err != nil {
			return nil, err
		}
		slot := &journal.slots[index]
		slot.batch = batch
		slot.ack = make(chan error, 1)
		journal.available <- slot
	}
	journal.workersDone.Add(config.Writers)
	for range config.Writers {
		go journal.worker()
	}
	return journal, nil
}

// Submit transfers one complete batch into a fixed slot according to mode.
func (journal *Journal) Submit(ctx context.Context, batch *persistence.AuditBatch) error {
	if journal == nil || !journal.mode.Valid() {
		return persistence.ErrInvalidJournal
	}
	if journal.mode == persistence.AuditOff {
		return nil
	}
	if ctx == nil || batch == nil {
		return persistence.ErrInvalidJournal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-journal.stopping:
		return persistence.ErrJournalClosed
	default:
	}

	var slot *journalSlot
	if journal.mode == persistence.AuditBestEffort {
		select {
		case slot = <-journal.available:
		case <-journal.stopping:
			return persistence.ErrJournalClosed
		default:
			select {
			case <-journal.stopping:
				return persistence.ErrJournalClosed
			default:
				journal.dropped.Add(1)
				return persistence.ErrJournalQueueFull
			}
		}
	} else {
		select {
		case slot = <-journal.available:
		case <-ctx.Done():
			return ctx.Err()
		case <-journal.stopping:
			return persistence.ErrJournalClosed
		}
	}

	if err := persistence.CopyAuditBatch(&slot.batch, batch); err != nil {
		journal.available <- slot
		return err
	}
	if err := ctx.Err(); err != nil {
		journal.available <- slot
		return err
	}
	select {
	case <-journal.stopping:
		journal.available <- slot
		return persistence.ErrJournalClosed
	default:
	}

	slot.ctx = ctx
	required := journal.mode == persistence.AuditRequired
	slot.required = required
	journal.inFlight.Add(1)
	journal.accepted.Add(1)
	journal.jobs <- slot
	if !required {
		return nil
	}
	err := <-slot.ack
	slot.ctx = nil
	journal.available <- slot
	return err
}

// Stats returns counters without blocking submissions or writers.
func (journal *Journal) Stats() JournalStats {
	if journal == nil {
		return JournalStats{}
	}
	return JournalStats{
		Accepted:  journal.accepted.Load(),
		Succeeded: journal.succeeded.Load(),
		Failed:    journal.failed.Load(),
		Dropped:   journal.dropped.Load(),
		InFlight:  journal.inFlight.Load(),
	}
}

// Close rejects new admissions and drains every admitted slot. If ctx expires,
// the single shutdown sequence continues and a later Close may wait again.
func (journal *Journal) Close(ctx context.Context) error {
	if journal == nil || ctx == nil || !journal.mode.Valid() {
		return persistence.ErrInvalidJournal
	}
	journal.closeOnce.Do(func() {
		close(journal.stopping)
		go journal.shutdown()
	})
	select {
	case <-journal.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (journal *Journal) worker() {
	defer journal.workersDone.Done()
	for slot := range journal.jobs {
		parent := slot.ctx
		if !slot.required {
			parent = context.WithoutCancel(parent)
		}
		ctx, cancel := context.WithTimeout(parent, journal.writeTimeout)
		err := journal.store.Append(ctx, &slot.batch)
		cancel()
		if err == nil {
			journal.succeeded.Add(1)
		} else {
			journal.failed.Add(1)
		}
		journal.inFlight.Add(^uint64(0))
		if slot.required {
			slot.ack <- err
			continue
		}
		slot.ctx = nil
		journal.available <- slot
	}
}

func (journal *Journal) shutdown() {
	if journal.mode == persistence.AuditOff {
		close(journal.closed)
		return
	}
	for range journal.slots {
		<-journal.available
	}
	close(journal.jobs)
	journal.workersDone.Wait()
	close(journal.closed)
}
