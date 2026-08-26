package scheduler

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/result"
)

func TestSchedulerStressSaturationCancellationAndReuse(t *testing.T) {
	p, base := schedulerFixture(t)
	scheduler := newTestScheduler(t, 2, 2, 1)
	defer closeTestScheduler(t, scheduler)

	held := make([]Lease, scheduler.workers)
	for index := range held {
		lease, err := scheduler.arena.Borrow(context.Background())
		if err != nil {
			t.Fatalf("Borrow(%d) error = %v", index, err)
		}
		held[index] = lease
	}

	batch := repeatSchedulerBatch(t, p, base, 129)
	activeContexts := make([]context.CancelFunc, scheduler.queueDepth)
	activeDone := make(chan error, scheduler.queueDepth)
	for index := range activeContexts {
		ctx, cancel := context.WithCancel(context.Background())
		activeContexts[index] = cancel
		go func() {
			var dst result.Batch
			activeDone <- scheduler.Execute(ctx, &dst, p, batch)
		}()
	}
	schedulerAwait(t, func() bool {
		return len(scheduler.available) == 0 && len(scheduler.workTokens) == 0
	})

	queuedBase, queuedCancel := context.WithCancel(context.Background())
	queuedContext := &schedulerObservedContext{Context: queuedBase, observed: make(chan struct{})}
	queuedDone := make(chan error, 1)
	go func() {
		var dst result.Batch
		queuedDone <- scheduler.Execute(queuedContext, &dst, p, batch)
	}()
	<-queuedContext.observed
	queuedCancel()
	if err := <-queuedDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("saturated queue cancellation error = %v", err)
	}

	for _, cancel := range activeContexts {
		cancel()
	}
	for _, lease := range held {
		if err := scheduler.arena.Return(lease); err != nil {
			t.Fatalf("Return held lease error = %v", err)
		}
	}
	for range activeContexts {
		if err := <-activeDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("active cancellation error = %v", err)
		}
	}
	if len(scheduler.available) != scheduler.queueDepth || len(scheduler.workTokens) != scheduler.workers {
		t.Fatalf("recovered budgets = states:%d/%d tokens:%d/%d",
			len(scheduler.available), scheduler.queueDepth,
			len(scheduler.workTokens), scheduler.workers,
		)
	}

	var direct eval.Executor
	var got result.Batch
	for iteration, rows := range []uint32{513, 1, 65, 0, 256, 63, 129, 513} {
		current := repeatSchedulerBatch(t, p, base, rows)
		var want result.Batch
		if err := direct.Execute(&want, p, current); err != nil {
			t.Fatalf("direct iteration %d rows=%d error = %v", iteration, rows, err)
		}
		for stateIndex := range scheduler.states {
			state := &scheduler.states[stateIndex]
			for shardIndex := range state.results {
				poisonSchedulerResult(&state.results[shardIndex])
				state.errors[shardIndex] = errors.New("poison")
			}
		}
		if err := scheduler.Execute(context.Background(), &got, p, current); err != nil {
			t.Fatalf("Execute iteration %d rows=%d error = %v", iteration, rows, err)
		}
		if !reflect.DeepEqual(got, want) {
			assertSchedulerResult(t, got, want)
		}
	}
}

func poisonSchedulerResult(batch *result.Batch) {
	value := reflect.ValueOf(batch).Elem()
	for field := range value.NumField() {
		column := value.Field(field)
		if column.Kind() != reflect.Slice || column.Cap() == 0 {
			continue
		}
		length := column.Len()
		column.SetLen(column.Cap())
		for row := range column.Len() {
			item := column.Index(row)
			switch item.Kind() {
			case reflect.Bool:
				item.SetBool(true)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				item.SetInt(-1)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				item.SetUint(^uint64(0))
			}
		}
		column.SetLen(length)
	}
	batch.Rows = ^uint32(0)
}
