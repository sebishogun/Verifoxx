package scheduler

import (
	"context"
	"errors"
	"os"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestSchedulerShardHelpers(t *testing.T) {
	t.Run("word balanced ranges", func(t *testing.T) {
		tests := []struct {
			rows   uint32
			shards int
			want   []rowRange
		}{
			{0, 4, []rowRange{{0, 0}}},
			{63, 4, []rowRange{{0, 63}}},
			{64, 4, []rowRange{{0, 64}}},
			{65, 4, []rowRange{{0, 64}, {64, 65}}},
			{127, 4, []rowRange{{0, 64}, {64, 127}}},
			{128, 4, []rowRange{{0, 64}, {64, 128}}},
			{129, 3, []rowRange{{0, 64}, {64, 128}, {128, 129}}},
			{513, 3, []rowRange{{0, 192}, {192, 384}, {384, 513}}},
		}
		storage := make([]rowRange, 4)
		for _, tc := range tests {
			got := partitionRows(storage, tc.rows, tc.shards)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("partitionRows(%d,%d) = %+v, want %+v", tc.rows, tc.shards, got, tc.want)
			}
		}
		if got := partitionRows(nil, 129, 3); len(got) != 0 {
			t.Fatalf("partitionRows(nil) = %+v, want empty", got)
		}
	})

	t.Run("deterministic full result merge", func(t *testing.T) {
		ranges := partitionRows(make([]rowRange, 3), 129, 3)
		shards := make([]result.Batch, len(ranges))
		for _, shard := range [...]int{2, 0, 1} {
			shards[shard] = schedulerTestResult(t, ranges[shard].start, ranges[shard].end)
		}
		want := schedulerTestResult(t, 0, 129)
		var got result.Batch
		if err := mergeResults(&got, shards, ranges, 129); err != nil {
			t.Fatalf("mergeResults: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("merged result differs\ngot:  %+v\nwant: %+v", got, want)
		}

		var mergeErr error
		if allocs := testing.AllocsPerRun(100, func() {
			mergeErr = mergeResults(&got, shards, ranges, 129)
		}); allocs != 0 {
			t.Fatalf("warm merge allocations = %g, want 0", allocs)
		}
		if mergeErr != nil {
			t.Fatal(mergeErr)
		}
	})

	t.Run("zero rows", func(t *testing.T) {
		var shard result.Batch
		if err := shard.Reset(0); err != nil {
			t.Fatal(err)
		}
		var got result.Batch
		if err := mergeResults(&got, []result.Batch{shard}, []rowRange{{0, 0}}, 0); err != nil {
			t.Fatalf("mergeResults zero rows: %v", err)
		}
		want := schedulerTestResult(t, 0, 0)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("zero-row merge = %+v, want %+v", got, want)
		}
	})

	t.Run("invalid input is atomic", func(t *testing.T) {
		ranges := []rowRange{{0, 64}, {64, 65}}
		shards := []result.Batch{
			schedulerTestResult(t, 0, 64),
			schedulerTestResult(t, 64, 65),
		}
		tests := []struct {
			name   string
			mutate func([]result.Batch, []rowRange) ([]result.Batch, []rowRange)
		}{
			{"shard range count", func(s []result.Batch, r []rowRange) ([]result.Batch, []rowRange) {
				return s[:1], r
			}},
			{"range gap", func(s []result.Batch, r []rowRange) ([]result.Batch, []rowRange) {
				r[1].start++
				return s, r
			}},
			{"shard rows", func(s []result.Batch, r []rowRange) ([]result.Batch, []rowRange) {
				s[0].Rows--
				return s, r
			}},
			{"outcomes", func(s []result.Batch, r []rowRange) ([]result.Batch, []rowRange) {
				s[0].OutcomeIDs = s[0].OutcomeIDs[:len(s[0].OutcomeIDs)-1]
				return s, r
			}},
			{"offset length", func(s []result.Batch, r []rowRange) ([]result.Batch, []rowRange) {
				s[0].RequirementOffsets = s[0].RequirementOffsets[:len(s[0].RequirementOffsets)-1]
				return s, r
			}},
			{"offset start", func(s []result.Batch, r []rowRange) ([]result.Batch, []rowRange) {
				s[0].EvidenceOffsets[0] = 1
				return s, r
			}},
			{"offset order", func(s []result.Batch, r []rowRange) ([]result.Batch, []rowRange) {
				s[0].ReasonOffsets[2] = s[0].ReasonOffsets[1] - 1
				return s, r
			}},
			{"offset final", func(s []result.Batch, r []rowRange) ([]result.Batch, []rowRange) {
				s[0].RemediationIDs = append(s[0].RemediationIDs, 1)
				return s, r
			}},
			{"driver columns", func(s []result.Batch, r []rowRange) ([]result.Batch, []rowRange) {
				s[0].DriverReasons = s[0].DriverReasons[:len(s[0].DriverReasons)-1]
				return s, r
			}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				badShards := []result.Batch{cloneSchedulerResult(shards[0]), cloneSchedulerResult(shards[1])}
				badRanges := slices.Clone(ranges)
				badShards, badRanges = tc.mutate(badShards, badRanges)
				dst := schedulerTestResult(t, 20, 27)
				want := cloneSchedulerResult(dst)
				if err := mergeResults(&dst, badShards, badRanges, 65); err == nil {
					t.Fatal("mergeResults error = nil")
				}
				if !reflect.DeepEqual(dst, want) {
					t.Fatalf("invalid merge mutated destination\ngot:  %+v\nwant: %+v", dst, want)
				}
			})
		}
		if _, ok := addMergeEdges(mergeEdgeLimit(), 1); ok {
			t.Fatal("edge total overflow accepted")
		}
	})
}

func TestScheduler(t *testing.T) {
	p, base := schedulerFixture(t)

	t.Run("invalid construction and calls", func(t *testing.T) {
		for _, config := range []Config{
			{},
			{Workers: 1},
			{Workers: -1, QueueDepth: 1},
			{Workers: 1, QueueDepth: -1},
			{Workers: 1, QueueDepth: 1, Capacity: Capacity{InputBytes: -1}},
		} {
			if scheduler, err := NewScheduler(config); !errors.Is(err, ErrInvalidScheduler) || scheduler != nil {
				t.Fatalf("NewScheduler(%+v) = (%v,%v), want nil %v", config, scheduler, err, ErrInvalidScheduler)
			}
		}

		var nilScheduler *Scheduler
		var dst result.Batch
		if err := nilScheduler.Execute(context.Background(), &dst, p, base); !errors.Is(err, ErrInvalidScheduler) {
			t.Fatalf("nil Execute error = %v", err)
		}
		if err := nilScheduler.Close(); !errors.Is(err, ErrInvalidScheduler) {
			t.Fatalf("nil Close error = %v", err)
		}

		scheduler := newTestScheduler(t, 2, 2, 1)
		defer closeTestScheduler(t, scheduler)
		if err := scheduler.Execute(nil, &dst, p, base); !errors.Is(err, ErrInvalidContext) {
			t.Fatalf("nil context error = %v", err)
		}
		if err := scheduler.Execute(context.Background(), nil, p, base); !errors.Is(err, ErrInvalidScheduler) {
			t.Fatalf("nil destination error = %v", err)
		}
		if err := scheduler.Execute(context.Background(), &dst, nil, base); !errors.Is(err, ErrInvalidScheduler) {
			t.Fatalf("nil program error = %v", err)
		}
	})

	t.Run("serial and parallel equivalence", func(t *testing.T) {
		serial := newTestScheduler(t, 4, 1, ^uint32(0))
		parallel := newTestScheduler(t, 4, 1, 1)
		defer closeTestScheduler(t, serial)
		defer closeTestScheduler(t, parallel)

		for _, rows := range [...]uint32{0, 63, 64, 65, 127, 128, 129, 256} {
			batch := repeatSchedulerBatch(t, p, base, rows)
			var executor eval.Executor
			var want result.Batch
			if err := executor.Execute(&want, p, batch); err != nil {
				t.Fatalf("direct rows=%d: %v", rows, err)
			}
			for name, scheduler := range map[string]*Scheduler{"serial": serial, "parallel": parallel} {
				var got result.Batch
				for run := 0; run < 3; run++ {
					if err := scheduler.Execute(context.Background(), &got, p, batch); err != nil {
						t.Fatalf("%s rows=%d run=%d: %v", name, rows, run, err)
					}
					assertSchedulerResult(t, got, want)
				}
			}

			wantRanges := partitionRows(make([]rowRange, 4), rows, 4)
			state := &parallel.states[0]
			if !slices.Equal(state.ranges[:state.shardCount], wantRanges) {
				t.Fatalf("rows=%d ranges = %+v, want %+v", rows, state.ranges[:state.shardCount], wantRanges)
			}
		}
	})

	t.Run("automatic crossover", func(t *testing.T) {
		scheduler := newTestScheduler(t, 4, 1, 0)
		defer closeTestScheduler(t, scheduler)
		for _, test := range []struct {
			rows       uint32
			wantShards int
		}{{defaultParallelRows - 1, 1}, {defaultParallelRows, 4}, {defaultParallelRows + 1, 4}} {
			batch := repeatSchedulerBatch(t, p, base, test.rows)
			var dst result.Batch
			if err := scheduler.Execute(context.Background(), &dst, p, batch); err != nil {
				t.Fatalf("Execute rows=%d: %v", test.rows, err)
			}
			if got := scheduler.states[0].shardCount; got != test.wantShards {
				t.Fatalf("rows=%d shards=%d, want %d", test.rows, got, test.wantShards)
			}
		}
	})

	t.Run("bounded admission and cancellation", func(t *testing.T) {
		scheduler := newTestScheduler(t, 2, 1, 1)
		defer closeTestScheduler(t, scheduler)
		if cap(scheduler.available) != 1 || len(scheduler.available) != 1 ||
			cap(scheduler.workTokens) != 2 || len(scheduler.workTokens) != 2 || cap(scheduler.jobs) != 2 {
			t.Fatalf("fixed channel sizes available=%d/%d tokens=%d/%d jobs=%d", len(scheduler.available), cap(scheduler.available), len(scheduler.workTokens), cap(scheduler.workTokens), cap(scheduler.jobs))
		}

		heldState := <-scheduler.available
		queuedBase, queuedCancel := context.WithCancel(context.Background())
		queued := &schedulerObservedContext{Context: queuedBase, observed: make(chan struct{})}
		queuedDone := make(chan error, 1)
		go func() {
			var dst result.Batch
			queuedDone <- scheduler.Execute(queued, &dst, p, base)
		}()
		<-queued.observed
		queuedCancel()
		if err := <-queuedDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancellation error = %v", err)
		}
		scheduler.available <- heldState

		first := <-scheduler.workTokens
		second := <-scheduler.workTokens
		tokenBase, tokenCancel := context.WithCancel(context.Background())
		tokenCtx := &schedulerObservedContext{Context: tokenBase, observed: make(chan struct{})}
		tokenDone := make(chan error, 1)
		go func() {
			var dst result.Batch
			tokenDone <- scheduler.Execute(tokenCtx, &dst, p, base)
		}()
		<-tokenCtx.observed
		tokenCancel()
		if err := <-tokenDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("token cancellation error = %v", err)
		}
		scheduler.workTokens <- first
		scheduler.workTokens <- second

		preCanceled, cancel := context.WithCancel(context.Background())
		cancel()
		var dst result.Batch
		if err := scheduler.Execute(preCanceled, &dst, p, base); !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-canceled error = %v", err)
		}
	})

	t.Run("global work budget", func(t *testing.T) {
		scheduler := newTestScheduler(t, 4, 2, 1)
		defer closeTestScheduler(t, scheduler)
		leases := make([]Lease, 4)
		for i := range leases {
			lease, err := scheduler.arena.Borrow(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			leases[i] = lease
		}
		batch := repeatSchedulerBatch(t, p, base, 129)
		contexts := make([]context.CancelFunc, 2)
		done := make(chan error, 2)
		for i := range contexts {
			ctx, cancel := context.WithCancel(context.Background())
			contexts[i] = cancel
			go func() {
				var dst result.Batch
				done <- scheduler.Execute(ctx, &dst, p, batch)
			}()
		}
		schedulerAwait(t, func() bool { return len(scheduler.workTokens) == 0 })
		for _, cancel := range contexts {
			cancel()
		}
		for _, lease := range leases {
			if err := scheduler.arena.Return(lease); err != nil {
				t.Fatal(err)
			}
		}
		for range contexts {
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("budgeted Execute error = %v", err)
			}
		}
		if len(scheduler.workTokens) != 4 {
			t.Fatalf("returned work tokens = %d, want 4", len(scheduler.workTokens))
		}
	})

	t.Run("failure atomicity reuse and warm allocations", func(t *testing.T) {
		scheduler := newTestScheduler(t, 4, 1, 1)
		defer closeTestScheduler(t, scheduler)
		batch := repeatSchedulerBatch(t, p, base, 256)
		var direct eval.Executor
		var want result.Batch
		if err := direct.Execute(&want, p, batch); err != nil {
			t.Fatal(err)
		}

		bad := *p
		bad.Opcodes = slices.Clone(p.Opcodes)
		bad.Opcodes[0] = program.Opcode(255)
		dst := schedulerTestResult(t, 0, 7)
		poison := cloneSchedulerResult(dst)
		wantReuse := cloneSchedulerResult(poison)
		if err := direct.Execute(&wantReuse, p, batch); err != nil {
			t.Fatal(err)
		}
		if err := scheduler.Execute(context.Background(), &dst, &bad, batch); err == nil {
			t.Fatal("malformed program error = nil")
		}
		if !reflect.DeepEqual(dst, poison) {
			t.Fatal("evaluator failure mutated destination")
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := scheduler.Execute(canceled, &dst, p, batch); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Execute error = %v", err)
		}
		if !reflect.DeepEqual(dst, poison) {
			t.Fatal("canceled Execute mutated destination")
		}

		state := &scheduler.states[0]
		for i := range state.results {
			state.results[i] = schedulerTestResult(t, uint32(i), uint32(i+3))
			state.errors[i] = errors.New("poison")
		}
		if err := scheduler.Execute(context.Background(), &dst, p, batch); err != nil {
			t.Fatalf("poisoned state reuse: %v", err)
		}
		if !reflect.DeepEqual(dst, wantReuse) {
			assertSchedulerResult(t, dst, wantReuse)
		}

		primeScheduler(t, scheduler, p, batch)
		if err := scheduler.Execute(context.Background(), &dst, p, batch); err != nil {
			t.Fatalf("warmup Execute: %v", err)
		}
		var executeErr error
		if allocs := testing.AllocsPerRun(100, func() {
			executeErr = scheduler.Execute(context.Background(), &dst, p, batch)
		}); allocs != 0 {
			t.Fatalf("warm Scheduler.Execute allocations = %g, want 0", allocs)
		}
		if executeErr != nil {
			t.Fatal(executeErr)
		}
	})

	t.Run("graceful idempotent close", func(t *testing.T) {
		scheduler := newTestScheduler(t, 2, 1, 1)
		held := <-scheduler.available
		first := make(chan error, 1)
		second := make(chan error, 1)
		go func() { first <- scheduler.Close() }()
		<-scheduler.stopping
		go func() { second <- scheduler.Close() }()
		scheduler.available <- held
		if err := <-first; err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := <-second; err != nil {
			t.Fatalf("second Close: %v", err)
		}
		if err := scheduler.Close(); err != nil {
			t.Fatalf("third Close: %v", err)
		}
		var dst result.Batch
		if err := scheduler.Execute(context.Background(), &dst, p, base); !errors.Is(err, ErrSchedulerClosed) {
			t.Fatalf("Execute after Close error = %v", err)
		}
	})
}

func schedulerTestResult(t testing.TB, start, end uint32) result.Batch {
	t.Helper()
	var batch result.Batch
	if err := batch.Reset(end - start); err != nil {
		t.Fatal(err)
	}
	for row := start; row < end; row++ {
		local := row - start
		batch.OutcomeIDs[local] = schema.OutcomeID(row%4 + 1)
		if row%3 != 0 {
			batch.RequirementIDs = append(batch.RequirementIDs, schema.RequirementID(row%5+1))
		}
		if row%5 == 0 {
			batch.RequirementIDs = append(batch.RequirementIDs, schema.RequirementID(row%7+1))
		}
		batch.RequirementOffsets[local+1] = uint32(len(batch.RequirementIDs))

		if row&1 != 0 {
			batch.DriverRequirements = append(batch.DriverRequirements, schema.RequirementID(row%5+1))
			batch.DriverClauses = append(batch.DriverClauses, schema.ClauseID(row%3+1))
			batch.DriverNodes = append(batch.DriverNodes, schema.NodeID(row%7+1))
			batch.DriverReasons = append(batch.DriverReasons, schema.ReasonID(row%11+1))
			batch.DriverExplanations = append(batch.DriverExplanations, schema.ExplanationID(row%13+1))
		}
		batch.DriverOffsets[local+1] = uint32(len(batch.DriverRequirements))

		if row%4 == 0 {
			batch.EvidenceIDs = append(batch.EvidenceIDs, schema.EvidenceID(row%9+1))
		}
		batch.EvidenceOffsets[local+1] = uint32(len(batch.EvidenceIDs))
		if row%6 == 0 {
			batch.ReasonIDs = append(batch.ReasonIDs, schema.ReasonID(row%8+1))
			batch.ReasonNodes = append(batch.ReasonNodes, schema.NodeID(row%10+1))
			if row%12 == 0 {
				batch.ReasonEvidenceIDs = append(batch.ReasonEvidenceIDs, 0)
				batch.ReasonEvidenceStates = append(batch.ReasonEvidenceStates, 0)
			} else {
				batch.ReasonEvidenceIDs = append(batch.ReasonEvidenceIDs, schema.EvidenceID(row%9+1))
				batch.ReasonEvidenceStates = append(batch.ReasonEvidenceStates, schema.EvidenceStateID(row%5+1))
			}
		}
		batch.ReasonOffsets[local+1] = uint32(len(batch.ReasonIDs))
		if row%7 == 0 {
			batch.RemediationIDs = append(batch.RemediationIDs, schema.RemediationID(row%6+1))
		}
		batch.RemediationOffsets[local+1] = uint32(len(batch.RemediationIDs))
	}
	return batch
}

func cloneSchedulerResult(source result.Batch) result.Batch {
	clone := source
	clone.OutcomeIDs = slices.Clone(source.OutcomeIDs)
	clone.RequirementOffsets = slices.Clone(source.RequirementOffsets)
	clone.RequirementIDs = slices.Clone(source.RequirementIDs)
	clone.DriverOffsets = slices.Clone(source.DriverOffsets)
	clone.DriverRequirements = slices.Clone(source.DriverRequirements)
	clone.DriverClauses = slices.Clone(source.DriverClauses)
	clone.DriverNodes = slices.Clone(source.DriverNodes)
	clone.DriverReasons = slices.Clone(source.DriverReasons)
	clone.DriverExplanations = slices.Clone(source.DriverExplanations)
	clone.EvidenceOffsets = slices.Clone(source.EvidenceOffsets)
	clone.EvidenceIDs = slices.Clone(source.EvidenceIDs)
	clone.ReasonOffsets = slices.Clone(source.ReasonOffsets)
	clone.ReasonIDs = slices.Clone(source.ReasonIDs)
	clone.ReasonNodes = slices.Clone(source.ReasonNodes)
	clone.ReasonEvidenceIDs = slices.Clone(source.ReasonEvidenceIDs)
	clone.ReasonEvidenceStates = slices.Clone(source.ReasonEvidenceStates)
	clone.RemediationOffsets = slices.Clone(source.RemediationOffsets)
	clone.RemediationIDs = slices.Clone(source.RemediationIDs)
	return clone
}

func assertSchedulerResult(t testing.TB, got, want result.Batch) {
	t.Helper()
	gotValue := reflect.ValueOf(got)
	wantValue := reflect.ValueOf(want)
	resultType := gotValue.Type()
	for field := range gotValue.NumField() {
		gotField := gotValue.Field(field)
		wantField := wantValue.Field(field)
		equal := gotField.Kind() != reflect.Slice || gotField.Len() == wantField.Len()
		if equal && gotField.Kind() == reflect.Slice {
			for item := range gotField.Len() {
				if !reflect.DeepEqual(gotField.Index(item).Interface(), wantField.Index(item).Interface()) {
					equal = false
					break
				}
			}
		} else if equal {
			equal = reflect.DeepEqual(gotField.Interface(), wantField.Interface())
		}
		if !equal {
			t.Fatalf("result field %s differs\ngot:  %v\nwant: %v", resultType.Field(field).Name, gotValue.Field(field).Interface(), wantValue.Field(field).Interface())
		}
	}
}

type schedulerObservedContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (ctx *schedulerObservedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

func schedulerAwait(t testing.TB, condition func() bool) {
	t.Helper()
	for attempt := 0; attempt < 1_000_000; attempt++ {
		if condition() {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("scheduler condition not reached")
}

func schedulerFixture(t testing.TB) (*program.Program, eval.Batch) {
	t.Helper()
	symbols := schema.NewSymbolInterner(16)
	fields := schema.NewBuilder()
	for _, field := range []struct {
		name  string
		group schema.FieldGroup
	}{
		{"subject.trust", schema.FieldGroupSubject},
		{"context.environment", schema.FieldGroupContext},
		{"context.usage", schema.FieldGroupContext},
	} {
		name, err := symbols.Intern([]byte(field.name))
		if err != nil {
			t.Fatal(err)
		}
		kind := schema.ValueKindSymbol
		if field.name == "context.environment" {
			kind = schema.ValueKindPresence
		}
		if _, err := fields.AddField(name, kind, field.group); err != nil {
			t.Fatal(err)
		}
	}
	policySource, err := os.ReadFile("../../testdata/policies/valid-full.json")
	if err != nil {
		t.Fatal(err)
	}
	policyBuilder := ast.NewBuilder(ast.Hints{
		Nodes: 8, CompareNodes: 4, CompareListValues: 4, GroupNodes: 2,
		ChildEdges: 4, NotNodes: 1, EvidenceNodes: 2,
		Values: 16, SymbolValues: 16, SymbolBytes: 512,
		EvidenceKinds: 4, EvidenceStates: 8, Outcomes: 8,
		Remediations: 4, Clauses: 2, ClauseEvidenceEdges: 2,
		ClauseRemediationEdges: 4, Requirements: 2, RequirementClauseEdges: 2,
		SourceBytes: len(policySource),
	})
	fieldSchema := fields.Finish()
	if err := jsonpolicy.Decode(policyBuilder, policySource, fieldSchema, symbols, jsonpolicy.Limits{}); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	compiled, err := compile.Lower(policyBuilder.Document(), fieldSchema, symbols)
	if err != nil {
		t.Fatalf("compile policy: %v", err)
	}
	external, ok := compiled.LookupSymbol([]byte("external"))
	if !ok {
		t.Fatal("compiled policy omits external symbol")
	}
	standard, ok := compiled.LookupSymbol([]byte("standard"))
	if !ok {
		t.Fatal("compiled policy omits standard symbol")
	}
	approvalRecord, ok := compiled.LookupSymbol([]byte("approval_record"))
	if !ok {
		t.Fatal("compiled policy omits approval_record symbol")
	}
	approvalIndex := slices.Index(compiled.EvidenceKindNames, approvalRecord)
	if approvalIndex < 0 {
		t.Fatal("compiled policy omits approval_record evidence kind")
	}
	current, ok := compiled.LookupSymbol([]byte("current"))
	if !ok {
		t.Fatal("compiled policy omits current symbol")
	}
	currentIndex := slices.Index(compiled.EvidenceStateNames, current)
	if currentIndex < 0 {
		t.Fatal("compiled policy omits current evidence state")
	}
	stale, ok := compiled.LookupSymbol([]byte("stale"))
	if !ok {
		t.Fatal("compiled policy omits stale symbol")
	}
	staleIndex := slices.Index(compiled.EvidenceStateNames, stale)
	if staleIndex < 0 {
		t.Fatal("compiled policy omits stale evidence state")
	}
	approvalKind := schema.EvidenceKindID(approvalIndex + 1)
	currentState := schema.EvidenceStateID(currentIndex + 1)
	staleState := schema.EvidenceStateID(staleIndex + 1)
	var batchBuilder eval.Builder
	if err := batchBuilder.Begin(compiled, 3, 2, 2); err != nil {
		t.Fatalf("begin source batch: %v", err)
	}
	for row := uint32(0); row < 3; row++ {
		if err := batchBuilder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatalf("set source request %d: %v", row, err)
		}
		if err := batchBuilder.SetSymbol(row, 1, external); err != nil {
			t.Fatalf("set source trust %d: %v", row, err)
		}
		if err := batchBuilder.SetPresent(row, 2); err != nil {
			t.Fatalf("set source environment %d: %v", row, err)
		}
		if err := batchBuilder.SetSymbol(row, 3, standard); err != nil {
			t.Fatalf("set source usage %d: %v", row, err)
		}
	}
	if err := batchBuilder.SetEvidence(0, eval.EvidenceRecord{ID: 1, Kind: approvalKind, State: currentState}); err != nil {
		t.Fatalf("set current source evidence: %v", err)
	}
	if err := batchBuilder.SetEvidence(1, eval.EvidenceRecord{ID: 2, Kind: approvalKind, State: staleState}); err != nil {
		t.Fatalf("set stale source evidence: %v", err)
	}
	if err := batchBuilder.SetEvidenceCSR([]uint32{0, 1, 2, 2}, []uint32{0, 1}); err != nil {
		t.Fatalf("set source evidence CSR: %v", err)
	}
	batch, err := batchBuilder.Finish()
	if err != nil {
		t.Fatalf("finish source batch: %v", err)
	}
	return compiled, batch
}

func repeatSchedulerBatch(t testing.TB, p *program.Program, source eval.Batch, rows uint32) eval.Batch {
	t.Helper()
	var referenceCount uint64
	for row := uint32(0); row < rows; row++ {
		start, end, ok := source.EvidenceRange(row % source.Rows)
		if !ok {
			t.Fatal("invalid source evidence CSR")
		}
		referenceCount += uint64(end - start)
	}
	if referenceCount > uint64(^uint32(0)) {
		t.Fatal("test evidence references overflow uint32")
	}

	var builder eval.Builder
	if err := builder.Begin(p, rows, uint32(source.Evidence.Len()), uint32(referenceCount)); err != nil {
		t.Fatalf("Begin repeated batch: %v", err)
	}
	for evidenceRow := range source.Evidence.IDs {
		record := eval.EvidenceRecord{
			Timestamp: source.Evidence.Timestamps[evidenceRow],
			ID:        source.Evidence.IDs[evidenceRow],
			Kind:      source.Evidence.Kinds[evidenceRow],
			State:     source.Evidence.States[evidenceRow],
			Subject:   source.Evidence.Subjects[evidenceRow],
			Scope:     source.Evidence.Scopes[evidenceRow],
			Reviewer:  source.Evidence.Reviewers[evidenceRow],
			Timing:    source.Evidence.Timings[evidenceRow],
		}
		if err := builder.SetEvidence(uint32(evidenceRow), record); err != nil {
			t.Fatalf("SetEvidence: %v", err)
		}
	}
	offsets := make([]uint32, int(rows)+1)
	refs := make([]uint32, 0, int(referenceCount))
	for row := uint32(0); row < rows; row++ {
		sourceRow := row % source.Rows
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatalf("SetRequestID: %v", err)
		}
		for fieldRow, kind := range p.FieldIndex.Kinds {
			field := schema.FieldID(fieldRow + 1)
			if !source.Present(field, sourceRow) {
				continue
			}
			column := p.FieldIndex.Columns[fieldRow]
			valueIndex := int(uint64(column)*uint64(source.Rows) + uint64(sourceRow))
			var err error
			switch kind {
			case schema.ValueKindSymbol:
				err = builder.SetSymbol(row, field, source.SymbolValues[valueIndex])
			case schema.ValueKindInteger:
				err = builder.SetInteger(row, field, source.IntegerValues[valueIndex])
			case schema.ValueKindTimestamp:
				err = builder.SetTimestamp(row, field, source.TimestampValues[valueIndex])
			case schema.ValueKindBoolean:
				err = builder.SetBoolean(row, field, source.Boolean(column, sourceRow))
			case schema.ValueKindPresence:
				err = builder.SetPresent(row, field)
			default:
				t.Fatalf("invalid field kind %d", kind)
			}
			if err != nil {
				t.Fatalf("set field %d: %v", field, err)
			}
		}
		start, end, _ := source.EvidenceRange(sourceRow)
		refs = append(refs, source.EvidenceRefs[start:end]...)
		offsets[row+1] = uint32(len(refs))
	}
	if err := builder.SetEvidenceCSR(offsets, refs); err != nil {
		t.Fatalf("SetEvidenceCSR: %v", err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish repeated batch: %v", err)
	}
	return batch
}

func newTestScheduler(t testing.TB, workers, queue int, parallelRows uint32) *Scheduler {
	t.Helper()
	scheduler, err := NewScheduler(Config{
		Capacity:     Capacity{Rows: 512},
		Workers:      workers,
		QueueDepth:   queue,
		ParallelRows: parallelRows,
	})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return scheduler
}

func closeTestScheduler(t testing.TB, scheduler *Scheduler) {
	t.Helper()
	if err := scheduler.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func primeScheduler(t testing.TB, scheduler *Scheduler, p *program.Program, batch eval.Batch) {
	t.Helper()
	ranges := partitionRows(make([]rowRange, scheduler.workers), batch.Rows, scheduler.workers)
	for stateIndex := range scheduler.states {
		state := &scheduler.states[stateIndex]
		for shardIndex, rows := range ranges {
			lease, err := scheduler.arena.Borrow(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := lease.Grow(Capacity{Rows: rows.end - rows.start}); err != nil {
				t.Fatal(err)
			}
			executor, err := lease.Executor()
			if err != nil {
				t.Fatal(err)
			}
			scratch := lease.context.evidenceOffsets[:int(rows.end-rows.start)+1]
			if err := executor.ExecuteRange(&state.results[shardIndex], p, batch, rows.start, rows.end, scratch); err != nil {
				t.Fatal(err)
			}
			if err := scheduler.arena.Return(lease); err != nil {
				t.Fatal(err)
			}
		}
	}
}
