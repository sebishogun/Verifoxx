package program

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRegistry(t *testing.T) {
	t.Run("empty reads", func(t *testing.T) {
		var registry Registry
		hash := registryHash(1)
		if got, found := registry.Lookup(hash); found || got != nil {
			t.Fatalf("empty Lookup = (%p,%v), want (nil,false)", got, found)
		}
		if got := registry.Active(); got != nil {
			t.Fatalf("empty Active = %p, want nil", got)
		}

		var nilRegistry *Registry
		if got, found := nilRegistry.Lookup(hash); found || got != nil {
			t.Fatalf("nil Lookup = (%p,%v), want (nil,false)", got, found)
		}
		if got := nilRegistry.Active(); got != nil {
			t.Fatalf("nil Active = %p, want nil", got)
		}
	})

	t.Run("errors are distinct", func(t *testing.T) {
		if ErrInvalidRegistry == nil || ErrPolicyNotFound == nil || ErrInvalidRegistry == ErrPolicyNotFound {
			t.Fatalf("registry errors = (%v,%v), want distinct nonnil sentinels", ErrInvalidRegistry, ErrPolicyNotFound)
		}
	})

	t.Run("publication is copy on write", func(t *testing.T) {
		var registry Registry
		first := registryProgram(1)
		canonical, err := registry.Publish(first)
		if err != nil {
			t.Fatalf("Publish first: %v", err)
		}
		if canonical != first {
			t.Fatalf("Publish first = %p, want %p", canonical, first)
		}
		if got, found := registry.Lookup(first.ContentHash); !found || got != first {
			t.Fatalf("Lookup first = (%p,%v), want (%p,true)", got, found, first)
		}
		firstSnapshot := registry.snapshot.Load()

		duplicate := registryProgram(1)
		duplicate.InputBytes[0] = 99
		canonical, err = registry.Publish(duplicate)
		if err != nil {
			t.Fatalf("Publish duplicate: %v", err)
		}
		if canonical != first || registry.snapshot.Load() != firstSnapshot {
			t.Fatalf("duplicate publication replaced canonical snapshot: program=%p snapshot=%p", canonical, registry.snapshot.Load())
		}

		second := registryProgram(2)
		if canonical, err = registry.Publish(second); err != nil || canonical != second {
			t.Fatalf("Publish second = (%p,%v), want (%p,nil)", canonical, err, second)
		}
		secondSnapshot := registry.snapshot.Load()
		if secondSnapshot == firstSnapshot {
			t.Fatal("second publication reused mutable snapshot")
		}
		if _, found := firstSnapshot.programs[second.ContentHash]; found {
			t.Fatal("second publication mutated old snapshot")
		}
		if got := secondSnapshot.programs[first.ContentHash]; got != first {
			t.Fatalf("new snapshot first = %p, want %p", got, first)
		}
		if got := secondSnapshot.programs[second.ContentHash]; got != second {
			t.Fatalf("new snapshot second = %p, want %p", got, second)
		}
	})

	t.Run("activation is explicit", func(t *testing.T) {
		var registry Registry
		first := registryProgram(1)
		second := registryProgram(2)
		if _, err := registry.Publish(first); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Publish(second); err != nil {
			t.Fatal(err)
		}
		if registry.Active() != nil {
			t.Fatal("publication changed active policy")
		}
		if err := registry.Activate(first.ContentHash); err != nil {
			t.Fatalf("Activate first: %v", err)
		}
		held := registry.Active()
		if held != first {
			t.Fatalf("Active first = %p, want %p", held, first)
		}
		if err := registry.Activate(second.ContentHash); err != nil {
			t.Fatalf("Activate second: %v", err)
		}
		if registry.Active() != second {
			t.Fatalf("Active second = %p, want %p", registry.Active(), second)
		}
		if held != first || len(held.InputBytes) != 1 || held.InputBytes[0] != 1 {
			t.Fatalf("held program changed after activation: %+v", held)
		}
	})

	t.Run("invalid mutations are atomic", func(t *testing.T) {
		var registry Registry
		published := registryProgram(1)
		if _, err := registry.Publish(published); err != nil {
			t.Fatal(err)
		}
		if err := registry.Activate(published.ContentHash); err != nil {
			t.Fatal(err)
		}
		wantSnapshot := registry.snapshot.Load()
		wantActive := registry.Active()

		var nilRegistry *Registry
		if _, err := nilRegistry.Publish(published); !errors.Is(err, ErrInvalidRegistry) {
			t.Fatalf("nil Publish error = %v, want %v", err, ErrInvalidRegistry)
		}
		if err := nilRegistry.Activate(published.ContentHash); !errors.Is(err, ErrInvalidRegistry) {
			t.Fatalf("nil Activate error = %v, want %v", err, ErrInvalidRegistry)
		}
		if _, err := registry.Publish(nil); !errors.Is(err, ErrInvalidRegistry) {
			t.Fatalf("nil candidate error = %v, want %v", err, ErrInvalidRegistry)
		}
		if _, err := registry.Publish(&Program{}); !errors.Is(err, ErrInvalidRegistry) {
			t.Fatalf("zero hash error = %v, want %v", err, ErrInvalidRegistry)
		}
		if err := registry.Activate([32]byte{}); !errors.Is(err, ErrPolicyNotFound) {
			t.Fatalf("zero activation error = %v, want %v", err, ErrPolicyNotFound)
		}
		if err := registry.Activate(registryHash(9)); !errors.Is(err, ErrPolicyNotFound) {
			t.Fatalf("absent activation error = %v, want %v", err, ErrPolicyNotFound)
		}
		if registry.snapshot.Load() != wantSnapshot || registry.Active() != wantActive {
			t.Fatal("invalid mutation changed registry state")
		}
	})

	t.Run("concurrent publication retains every hash", func(t *testing.T) {
		const count = 32
		var registry Registry
		var programs [count]*Program
		var publishErrors [count]error
		start := make(chan struct{})
		var done sync.WaitGroup
		done.Add(count)
		for i := range count {
			programs[i] = registryProgram(byte(i + 1))
			go func(index int) {
				defer done.Done()
				<-start
				_, publishErrors[index] = registry.Publish(programs[index])
			}(i)
		}
		close(start)
		done.Wait()
		for i, compiled := range programs {
			if publishErrors[i] != nil {
				t.Fatalf("Publish(%d): %v", i, publishErrors[i])
			}
			if got, found := registry.Lookup(compiled.ContentHash); !found || got != compiled {
				t.Fatalf("Lookup(%d) = (%p,%v), want (%p,true)", i, got, found, compiled)
			}
		}
	})

	t.Run("duplicate compilation runs once", func(t *testing.T) {
		const callers = 32
		var registry Registry
		compiled := registryProgram(1)
		start := make(chan struct{})
		release := make(chan struct{})
		var ready atomic.Int32
		var calls atomic.Int32
		var results [callers]*Program
		var compileErrors [callers]error
		var done sync.WaitGroup
		done.Add(callers)
		for caller := range callers {
			go func(index int) {
				defer done.Done()
				<-start
				ready.Add(1)
				results[index], compileErrors[index] = registry.LoadOrCompile(compiled.ContentHash, func() (*Program, error) {
					calls.Add(1)
					for ready.Load() != callers {
						runtime.Gosched()
					}
					<-release
					return compiled, nil
				})
			}(caller)
		}
		close(start)
		if !registryAwait(func() bool { return ready.Load() == callers && calls.Load() == 1 }) {
			close(release)
			done.Wait()
			t.Fatalf("before release ready=%d calls=%d, want %d and 1", ready.Load(), calls.Load(), callers)
		}
		close(release)
		done.Wait()
		if calls.Load() != 1 {
			t.Fatalf("compile calls = %d, want 1", calls.Load())
		}
		for caller := range callers {
			if compileErrors[caller] != nil || results[caller] != compiled {
				t.Fatalf("caller %d = (%p,%v), want (%p,nil)", caller, results[caller], compileErrors[caller], compiled)
			}
		}
	})

	t.Run("different hashes compile concurrently", func(t *testing.T) {
		var registry Registry
		programs := [2]*Program{registryProgram(1), registryProgram(2)}
		var results [2]*Program
		var compileErrors [2]error
		var entered atomic.Int32
		release := make(chan struct{})
		var done sync.WaitGroup
		done.Add(2)
		for i := range programs {
			go func(index int) {
				defer done.Done()
				results[index], compileErrors[index] = registry.LoadOrCompile(programs[index].ContentHash, func() (*Program, error) {
					entered.Add(1)
					<-release
					return programs[index], nil
				})
			}(i)
		}
		overlapped := registryAwait(func() bool { return entered.Load() == 2 })
		close(release)
		done.Wait()
		if !overlapped {
			t.Fatalf("concurrent compile entries = %d, want 2", entered.Load())
		}
		for i := range programs {
			if compileErrors[i] != nil || results[i] != programs[i] {
				t.Fatalf("compile %d = (%p,%v), want (%p,nil)", i, results[i], compileErrors[i], programs[i])
			}
		}
	})

	t.Run("direct publication wins compile race", func(t *testing.T) {
		var registry Registry
		compiled := registryProgram(1)
		compiled.InputBytes[0] = 99
		published := registryProgram(1)
		entered := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})
		var got *Program
		var compileErr error
		go func() {
			got, compileErr = registry.LoadOrCompile(compiled.ContentHash, func() (*Program, error) {
				close(entered)
				<-release
				return compiled, nil
			})
			close(done)
		}()
		<-entered
		canonical, err := registry.Publish(published)
		if err != nil || canonical != published {
			t.Fatalf("direct Publish = (%p,%v), want (%p,nil)", canonical, err, published)
		}
		close(release)
		<-done
		if compileErr != nil || got != published {
			t.Fatalf("LoadOrCompile = (%p,%v), want (%p,nil)", got, compileErr, published)
		}
	})

	t.Run("compile error fans out and permits retry", func(t *testing.T) {
		const callers = 16
		var registry Registry
		hash := registryHash(1)
		compileFailure := errors.New("compile failed")
		var calls atomic.Int32
		var results [callers]*Program
		var compileErrors [callers]error
		current := &compileFlight{done: make(chan struct{})}
		registry.flights = map[[32]byte]*compileFlight{hash: current}

		var done sync.WaitGroup
		done.Add(callers)
		for caller := range callers {
			go func(index int) {
				defer done.Done()
				results[index], compileErrors[index] = registry.LoadOrCompile(hash, func() (*Program, error) {
					calls.Add(1)
					return registryProgram(1), nil
				})
			}(caller)
		}
		current.err = compileFailure
		close(current.done)
		done.Wait()
		if calls.Load() != 0 {
			t.Fatalf("fan-out invoked compiler %d times, want 0", calls.Load())
		}
		for caller := range callers {
			if results[caller] != nil || !errors.Is(compileErrors[caller], compileFailure) {
				t.Fatalf("failed caller %d = (%p,%v), want (nil,%v)", caller, results[caller], compileErrors[caller], compileFailure)
			}
		}
		delete(registry.flights, hash)

		got, err := registry.LoadOrCompile(hash, func() (*Program, error) {
			calls.Add(1)
			return nil, compileFailure
		})
		if got != nil || !errors.Is(err, compileFailure) || calls.Load() != 1 {
			t.Fatalf("failed leader = (%p,%v) calls=%d, want (nil,%v) calls=1", got, err, calls.Load(), compileFailure)
		}
		if got, found := registry.Lookup(hash); found || got != nil {
			t.Fatalf("failed compile published (%p,%v)", got, found)
		}

		retry := registryProgram(1)
		got, err = registry.LoadOrCompile(hash, func() (*Program, error) {
			calls.Add(1)
			return retry, nil
		})
		if err != nil || got != retry || calls.Load() != 2 {
			t.Fatalf("retry = (%p,%v) calls=%d, want (%p,nil) calls=2", got, err, calls.Load(), retry)
		}
	})

	t.Run("invalid compile results are atomic", func(t *testing.T) {
		var registry Registry
		published := registryProgram(1)
		if _, err := registry.Publish(published); err != nil {
			t.Fatal(err)
		}
		if err := registry.Activate(published.ContentHash); err != nil {
			t.Fatal(err)
		}
		wantSnapshot := registry.snapshot.Load()
		wantActive := registry.Active()
		var callbackCalls int
		callback := func() (*Program, error) {
			callbackCalls++
			return registryProgram(2), nil
		}

		var nilRegistry *Registry
		if _, err := nilRegistry.LoadOrCompile(registryHash(2), callback); !errors.Is(err, ErrInvalidRegistry) {
			t.Fatalf("nil registry compile error = %v", err)
		}
		if _, err := registry.LoadOrCompile([32]byte{}, callback); !errors.Is(err, ErrInvalidRegistry) {
			t.Fatalf("zero hash compile error = %v", err)
		}
		if _, err := registry.LoadOrCompile(registryHash(2), nil); !errors.Is(err, ErrInvalidRegistry) {
			t.Fatalf("nil callback error = %v", err)
		}
		if callbackCalls != 0 {
			t.Fatalf("invalid request invoked callback %d times", callbackCalls)
		}
		if _, err := registry.LoadOrCompile(registryHash(2), func() (*Program, error) {
			return nil, nil
		}); !errors.Is(err, ErrInvalidRegistry) {
			t.Fatalf("nil result error = %v", err)
		}
		if _, err := registry.LoadOrCompile(registryHash(2), func() (*Program, error) {
			return registryProgram(3), nil
		}); !errors.Is(err, ErrInvalidRegistry) {
			t.Fatalf("hash mismatch error = %v", err)
		}
		if registry.snapshot.Load() != wantSnapshot || registry.Active() != wantActive {
			t.Fatal("invalid compile changed registry state")
		}
		if got, found := registry.Lookup(registryHash(2)); found || got != nil {
			t.Fatalf("invalid compile published (%p,%v)", got, found)
		}
	})

	t.Run("warm reads allocate zero", func(t *testing.T) {
		var registry Registry
		published := registryProgram(1)
		if _, err := registry.Publish(published); err != nil {
			t.Fatal(err)
		}
		if err := registry.Activate(published.ContentHash); err != nil {
			t.Fatal(err)
		}
		var got *Program
		var found bool
		if allocs := testing.AllocsPerRun(1000, func() {
			got, found = registry.Lookup(published.ContentHash)
		}); allocs != 0 {
			t.Fatalf("Lookup allocations = %g, want 0", allocs)
		}
		if !found || got != published {
			t.Fatalf("Lookup = (%p,%v), want (%p,true)", got, found, published)
		}
		if allocs := testing.AllocsPerRun(1000, func() {
			got = registry.Active()
		}); allocs != 0 {
			t.Fatalf("Active allocations = %g, want 0", allocs)
		}
		if got != published {
			t.Fatalf("Active = %p, want %p", got, published)
		}
	})
}

func registryHash(seed byte) [32]byte {
	var hash [32]byte
	hash[0] = seed
	return hash
}

func registryProgram(seed byte) *Program {
	return &Program{ContentHash: registryHash(seed), InputBytes: []byte{seed}}
}

func registryAwait(condition func() bool) bool {
	for range 1_000_000 {
		if condition() {
			return true
		}
		runtime.Gosched()
	}
	return false
}
