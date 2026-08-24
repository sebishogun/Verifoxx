// Package service coordinates bounded request admission independently of any
// transport or evaluator representation.
package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrInvalidService    = errors.New("service: invalid service")
	ErrInvalidContext    = errors.New("service: invalid context")
	ErrServiceStopping   = errors.New("service: stopping")
	ErrServiceBusy       = errors.New("service: busy")
	ErrInvalidAdmission  = errors.New("service: invalid admission")
	ErrAdmissionReleased = errors.New("service: admission released")
)

const MaxAdmissions = 4096

const (
	admissionAvailable uint32 = iota
	admissionActive
	admissionReturning
)

type admissionSlot struct {
	service    *Service
	generation atomic.Uint64
	state      atomic.Uint32
}

// Admission is one generation-stamped ownership claim on service capacity.
// It must be released after the complete request, including required audit.
type Admission struct {
	service    *Service
	slot       *admissionSlot
	generation uint64
}

// Stats is one bounded admission snapshot.
type Stats struct {
	Limit     int
	Active    int
	Queued    int
	Accepting bool
}

// Service owns a fixed slab of admission slots. No map or allocation occurs
// while requests wait, enter, or leave the service.
type Service struct {
	available     chan *admissionSlot
	stopping      chan struct{}
	drained       chan struct{}
	slots         []admissionSlot
	active        int
	queued        int
	mu            sync.Mutex
	stopOnce      sync.Once
	accepting     bool
	drainedClosed bool
}

// New allocates the complete in-flight request budget.
func New(limit int) (*Service, error) {
	if limit <= 0 || limit > MaxAdmissions {
		return nil, ErrInvalidService
	}
	service := &Service{
		available: make(chan *admissionSlot, limit),
		stopping:  make(chan struct{}),
		drained:   make(chan struct{}),
		slots:     make([]admissionSlot, limit),
		accepting: true,
	}
	for index := range service.slots {
		slot := &service.slots[index]
		slot.service = service
		slot.state.Store(admissionAvailable)
		service.available <- slot
	}
	return service, nil
}

func (service *Service) valid() bool {
	return service != nil && service.available != nil && service.stopping != nil && service.drained != nil &&
		len(service.slots) > 0 && cap(service.available) == len(service.slots)
}

// Admit waits for bounded request capacity. Shutdown wakes every queued caller
// without canceling requests that already hold an Admission.
func (service *Service) Admit(ctx context.Context) (Admission, error) {
	if !service.valid() {
		return Admission{}, ErrInvalidService
	}
	if ctx == nil {
		return Admission{}, ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return Admission{}, err
	}

	service.mu.Lock()
	if !service.accepting {
		service.mu.Unlock()
		return Admission{}, ErrServiceStopping
	}
	if service.queued >= len(service.slots) {
		service.mu.Unlock()
		return Admission{}, ErrServiceBusy
	}
	service.queued++
	service.mu.Unlock()

	var slot *admissionSlot
	var waitErr error
	select {
	case slot = <-service.available:
	case <-ctx.Done():
		waitErr = ctx.Err()
	case <-service.stopping:
		waitErr = ErrServiceStopping
	}

	service.mu.Lock()
	service.queued--
	if waitErr == nil {
		if err := ctx.Err(); err != nil {
			waitErr = err
		} else if !service.accepting {
			waitErr = ErrServiceStopping
		}
	}
	if waitErr != nil {
		if slot != nil {
			service.available <- slot
		}
		service.maybeCloseDrainedLocked()
		service.mu.Unlock()
		return Admission{}, waitErr
	}
	if slot == nil || slot.service != service {
		service.mu.Unlock()
		panic("service: invalid admission slot state")
	}
	generation := slot.generation.Add(1)
	if generation == 0 {
		generation = slot.generation.Add(1)
	}
	if !slot.state.CompareAndSwap(admissionAvailable, admissionActive) {
		service.mu.Unlock()
		panic("service: invalid admission slot state")
	}
	service.active++
	service.mu.Unlock()
	return Admission{service: service, slot: slot, generation: generation}, nil
}

// Release returns one active admission exactly once.
func (service *Service) Release(admission *Admission) error {
	if !service.valid() {
		return ErrInvalidService
	}
	if admission == nil || admission.service != service || admission.slot == nil || admission.generation == 0 ||
		admission.slot.service != service {
		return ErrInvalidAdmission
	}
	slot := admission.slot
	if slot.generation.Load() != admission.generation ||
		!slot.state.CompareAndSwap(admissionActive, admissionReturning) {
		return ErrAdmissionReleased
	}

	service.mu.Lock()
	service.active--
	if service.active < 0 {
		service.mu.Unlock()
		panic("service: negative active admission count")
	}
	slot.state.Store(admissionAvailable)
	service.available <- slot
	service.maybeCloseDrainedLocked()
	service.mu.Unlock()
	*admission = Admission{}
	return nil
}

// StopAdmission rejects new requests and wakes queued callers. Active
// admissions remain valid so evaluations and required audit writes can drain.
func (service *Service) StopAdmission() error {
	if !service.valid() {
		return ErrInvalidService
	}
	service.stopOnce.Do(func() {
		service.mu.Lock()
		service.accepting = false
		close(service.stopping)
		service.maybeCloseDrainedLocked()
		service.mu.Unlock()
	})
	return nil
}

// Drain stops admission and waits for queued and active calls to leave.
func (service *Service) Drain(ctx context.Context) error {
	if !service.valid() {
		return ErrInvalidService
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	if err := service.StopAdmission(); err != nil {
		return err
	}
	select {
	case <-service.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats reports admission state without exposing mutable service storage.
func (service *Service) Stats() Stats {
	if !service.valid() {
		return Stats{}
	}
	service.mu.Lock()
	stats := Stats{
		Limit:     len(service.slots),
		Active:    service.active,
		Queued:    service.queued,
		Accepting: service.accepting,
	}
	service.mu.Unlock()
	return stats
}

func (service *Service) maybeCloseDrainedLocked() {
	if !service.accepting && service.active == 0 && service.queued == 0 && !service.drainedClosed {
		service.drainedClosed = true
		close(service.drained)
	}
}
