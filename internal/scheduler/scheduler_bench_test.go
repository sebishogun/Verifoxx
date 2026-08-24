package scheduler

import (
	"context"
	"strconv"
	"testing"

	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/simdops"
)

func BenchmarkScheduler(b *testing.B) {
	p, base := schedulerFixture(b)
	runtime := simdops.Runtime()
	tier := runtime.Tier
	if runtime.PureGo {
		tier += "-purego"
	}
	tierName := "tier=" + tier
	for _, rows := range [...]uint32{64, 256, 1024, 4096, 16384} {
		batch := repeatSchedulerBatch(b, p, base, rows)
		rowName := "rows=" + strconv.FormatUint(uint64(rows), 10)
		for _, workers := range [...]int{1, 2, 4} {
			workerName := "workers=" + strconv.Itoa(workers)
			b.Run(tierName+"/"+rowName+"/"+workerName+"/direct", func(b *testing.B) {
				benchmarkSchedulerDirect(b, p, batch)
			})
			b.Run(tierName+"/"+rowName+"/"+workerName+"/scheduler-serial", func(b *testing.B) {
				benchmarkScheduledExecution(b, p, batch, workers, ^uint32(0))
			})
			b.Run(tierName+"/"+rowName+"/"+workerName+"/scheduler-parallel", func(b *testing.B) {
				benchmarkScheduledExecution(b, p, batch, workers, 1)
			})
		}
	}
}

func benchmarkSchedulerDirect(b *testing.B, p *program.Program, batch eval.Batch) {
	var executor eval.Executor
	var dst result.Batch
	if err := executor.Execute(&dst, p, batch); err != nil {
		b.Fatalf("prime direct Execute: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := executor.Execute(&dst, p, batch); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(batch.Rows), "rows")
}

func benchmarkScheduledExecution(b *testing.B, p *program.Program, batch eval.Batch, workers int, parallelRows uint32) {
	scheduler, err := NewScheduler(Config{
		Capacity:     Capacity{Rows: batch.Rows},
		Workers:      workers,
		QueueDepth:   1,
		ParallelRows: parallelRows,
	})
	if err != nil {
		b.Fatalf("NewScheduler: %v", err)
	}
	b.Cleanup(func() {
		if err := scheduler.Close(); err != nil {
			b.Errorf("Close: %v", err)
		}
	})

	primeScheduler(b, scheduler, p, batch)
	for range workers * 2 {
		var prime result.Batch
		if err := scheduler.Execute(context.Background(), &prime, p, batch); err != nil {
			b.Fatalf("prime scheduler Execute: %v", err)
		}
	}
	var direct eval.Executor
	var want result.Batch
	if err := direct.Execute(&want, p, batch); err != nil {
		b.Fatalf("direct Execute: %v", err)
	}
	var dst result.Batch
	if err := scheduler.Execute(context.Background(), &dst, p, batch); err != nil {
		b.Fatalf("scheduler Execute: %v", err)
	}
	assertSchedulerResult(b, dst, want)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := scheduler.Execute(context.Background(), &dst, p, batch); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(batch.Rows), "rows")
	b.ReportMetric(float64(workers), "workers")
}
