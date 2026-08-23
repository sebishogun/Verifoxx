package program

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	// ErrInvalidRegistry reports an invalid registry mutation or compile request.
	ErrInvalidRegistry = errors.New("program: invalid registry operation")
	// ErrPolicyNotFound reports activation of an unpublished content hash.
	ErrPolicyNotFound = errors.New("program: policy not found")
)

type registrySnapshot struct {
	programs map[[32]byte]*Program
}

// CompileFunc builds one frozen Program outside registry locks.
type CompileFunc func() (*Program, error)

type compileFlight struct {
	done     chan struct{}
	compiled *Program
	err      error
}

// Registry publishes immutable Programs through lock-free snapshots. Its zero
// value is an empty usable registry.
type Registry struct {
	snapshot atomic.Pointer[registrySnapshot]
	active   atomic.Pointer[Program]
	flights  map[[32]byte]*compileFlight
	publish  sync.Mutex
	flight   sync.Mutex
}

// Lookup returns the immutable Program published for hash.
func (registry *Registry) Lookup(hash [32]byte) (*Program, bool) {
	if registry == nil {
		return nil, false
	}
	snapshot := registry.snapshot.Load()
	if snapshot == nil {
		return nil, false
	}
	compiled, found := snapshot.programs[hash]
	return compiled, found
}

// Active returns the current default Program, or nil when none is selected.
func (registry *Registry) Active() *Program {
	if registry == nil {
		return nil
	}
	return registry.active.Load()
}

func validRegistryHash(hash [32]byte) bool {
	return hash != [32]byte{}
}

// Publish installs candidate in a new immutable snapshot. The first Program
// published for one content hash remains canonical.
func (registry *Registry) Publish(candidate *Program) (*Program, error) {
	if registry == nil || candidate == nil || !validRegistryHash(candidate.ContentHash) {
		return nil, ErrInvalidRegistry
	}
	hash := candidate.ContentHash
	registry.publish.Lock()
	current := registry.snapshot.Load()
	if current != nil {
		if existing, found := current.programs[hash]; found {
			registry.publish.Unlock()
			return existing, nil
		}
		if len(current.programs) == int(^uint(0)>>1) {
			registry.publish.Unlock()
			return nil, ErrInvalidRegistry
		}
	}
	size := 1
	if current != nil {
		size += len(current.programs)
	}
	programs := make(map[[32]byte]*Program, size)
	if current != nil {
		for existingHash, existing := range current.programs {
			programs[existingHash] = existing
		}
	}
	programs[hash] = candidate
	registry.snapshot.Store(&registrySnapshot{programs: programs})
	registry.publish.Unlock()
	return candidate, nil
}

// Activate selects an already-published Program as the default.
func (registry *Registry) Activate(hash [32]byte) error {
	if registry == nil {
		return ErrInvalidRegistry
	}
	if !validRegistryHash(hash) {
		return ErrPolicyNotFound
	}
	compiled, found := registry.Lookup(hash)
	if !found {
		return ErrPolicyNotFound
	}
	registry.active.Store(compiled)
	return nil
}

// LoadOrCompile returns a published Program or coordinates one compilation for
// an absent hash. Different hashes compile independently.
func (registry *Registry) LoadOrCompile(hash [32]byte, compile CompileFunc) (*Program, error) {
	if registry == nil || compile == nil || !validRegistryHash(hash) {
		return nil, ErrInvalidRegistry
	}
	if compiled, found := registry.Lookup(hash); found {
		return compiled, nil
	}

	registry.flight.Lock()
	if compiled, found := registry.Lookup(hash); found {
		registry.flight.Unlock()
		return compiled, nil
	}
	if existing := registry.flights[hash]; existing != nil {
		registry.flight.Unlock()
		<-existing.done
		return existing.compiled, existing.err
	}
	if registry.flights == nil {
		registry.flights = make(map[[32]byte]*compileFlight)
	}
	current := &compileFlight{done: make(chan struct{})}
	registry.flights[hash] = current
	registry.flight.Unlock()

	compiled, err := compile()
	if err != nil {
		compiled = nil
	} else if compiled == nil || compiled.ContentHash != hash {
		compiled = nil
		err = ErrInvalidRegistry
	} else {
		compiled, err = registry.Publish(compiled)
	}

	registry.flight.Lock()
	current.compiled = compiled
	current.err = err
	delete(registry.flights, hash)
	close(current.done)
	registry.flight.Unlock()
	return compiled, err
}
