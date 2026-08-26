package frontend

import (
	"context"
	"fmt"
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/frontend/cedar"
	"github.com/sebishogun/nornrune/frontend/cel"
	"github.com/sebishogun/nornrune/frontend/rego"
	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/scheduler"
	"github.com/sebishogun/nornrune/internal/schema"
)

var benchmarkFrontendSources = expressions(
	`team == "blue" && count >= 2 && enabled`,
	`input.team == "blue"; input.count >= 2; input.enabled`,
	`context.team == "blue" && context.count >= 2 && context.enabled`,
)

func BenchmarkFrontendParseCheck(b *testing.B) {
	limits := public.DefaultLimits()
	b.Run("cel", func(b *testing.B) {
		source := []byte(benchmarkFrontendSources.cel)
		bindings := conformanceBindings(public.LanguageCEL)
		b.ReportAllocs()
		for range b.N {
			parsed, diagnostics := cel.Parse(source, bindings, limits)
			if parsed == nil || len(diagnostics) != 0 {
				b.Fatalf("Parse = (%v, %+v)", parsed, diagnostics)
			}
		}
	})
	b.Run("rego", func(b *testing.B) {
		source := []byte(benchmarkFrontendSources.rego)
		bindings := conformanceBindings(public.LanguageRego)
		b.ReportAllocs()
		for range b.N {
			parsed, diagnostics := rego.Parse(source, bindings, limits)
			if parsed == nil || len(diagnostics) != 0 {
				b.Fatalf("Parse = (%v, %+v)", parsed, diagnostics)
			}
		}
	})
	b.Run("cedar", func(b *testing.B) {
		source := []byte(benchmarkFrontendSources.cedar)
		bindings := conformanceBindings(public.LanguageCedar)
		b.ReportAllocs()
		for range b.N {
			parsed, diagnostics := cedar.Parse(source, bindings, limits)
			if parsed == nil || len(diagnostics) != 0 {
				b.Fatalf("Parse = (%v, %+v)", parsed, diagnostics)
			}
		}
	})
}

func BenchmarkFrontendTranslate(b *testing.B) {
	limits := public.DefaultLimits()
	b.Run("cel", func(b *testing.B) {
		source := []byte(benchmarkFrontendSources.cel)
		bindings := conformanceBindings(public.LanguageCEL)
		parsed, diagnostics := cel.Parse(source, bindings, limits)
		if len(diagnostics) != 0 {
			b.Fatal(diagnostics)
		}
		b.ReportAllocs()
		for range b.N {
			policy, got := cel.Lower(source, parsed, bindings, limits)
			if policy == nil || len(got) != 0 {
				b.Fatalf("Lower = (%v, %+v)", policy, got)
			}
		}
	})
	b.Run("rego", func(b *testing.B) {
		source := []byte(benchmarkFrontendSources.rego)
		bindings := conformanceBindings(public.LanguageRego)
		parsed, diagnostics := rego.Parse(source, bindings, limits)
		if len(diagnostics) != 0 {
			b.Fatal(diagnostics)
		}
		b.ReportAllocs()
		for range b.N {
			policy, got := rego.Lower(source, parsed, bindings, limits)
			if policy == nil || len(got) != 0 {
				b.Fatalf("Lower = (%v, %+v)", policy, got)
			}
		}
	})
	b.Run("cedar", func(b *testing.B) {
		source := []byte(benchmarkFrontendSources.cedar)
		bindings := conformanceBindings(public.LanguageCedar)
		parsed, diagnostics := cedar.Parse(source, bindings, limits)
		if len(diagnostics) != 0 {
			b.Fatal(diagnostics)
		}
		b.ReportAllocs()
		for range b.N {
			policy, got := cedar.Lower(source, parsed, bindings, limits)
			if policy == nil || len(got) != 0 {
				b.Fatalf("Lower = (%v, %+v)", policy, got)
			}
		}
	})
}

func BenchmarkFrontendSharedLowering(b *testing.B) {
	policy, diagnostics := conformancePolicy(public.LanguageCEL, benchmarkFrontendSources)
	if policy == nil || len(diagnostics) != 0 {
		b.Fatalf("frontend Compile = (%v, %+v)", policy, diagnostics)
	}
	var compiler Compiler
	var compiled program.Program
	b.ReportAllocs()
	for range b.N {
		got, err := compiler.Compile(&compiled, policy)
		if err != nil || len(got) != 0 {
			b.Fatalf("Compile = (%+v, %v)", got, err)
		}
	}
}

func BenchmarkFrontendCompileCold(b *testing.B) {
	for _, language := range []public.Language{public.LanguageCEL, public.LanguageRego, public.LanguageCedar} {
		b.Run(language.String(), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				policy, diagnostics := conformancePolicy(language, benchmarkFrontendSources)
				if policy == nil || len(diagnostics) != 0 {
					b.Fatalf("frontend Compile = (%v, %+v)", policy, diagnostics)
				}
				compiled, diagnostics, err := Compile(policy)
				if compiled == nil || err != nil || len(diagnostics) != 0 {
					b.Fatalf("shared Compile = (%v, %+v, %v)", compiled, diagnostics, err)
				}
			}
		})
	}
}

func BenchmarkFrontendWarmEvaluation(b *testing.B) {
	compiled := compileConformancePolicy(b, public.LanguageCEL, benchmarkFrontendSources)
	for _, rows := range []uint32{1, 64, 256, 4096} {
		batch := benchmarkFrontendBatch(b, compiled, rows)
		b.Run(fmt.Sprintf("rows=%d/automatic", rows), func(b *testing.B) {
			var executor eval.Executor
			var dst result.Batch
			if err := executor.Execute(&dst, compiled, batch); err != nil {
				b.Fatal(err)
			}
			assertBenchmarkFrontendResults(b, dst.OutcomeIDs, rows)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := executor.Execute(&dst, compiled, batch); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("rows=%d/scheduled", rows), func(b *testing.B) {
			scheduled, err := scheduler.NewScheduler(scheduler.Config{
				Capacity: scheduler.Capacity{Rows: rows}, Workers: 1, QueueDepth: 1, ParallelRows: ^uint32(0),
			})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				if err := scheduled.Close(); err != nil {
					b.Error(err)
				}
			})
			var dst result.Batch
			if err := scheduled.Execute(context.Background(), &dst, compiled, batch); err != nil {
				b.Fatal(err)
			}
			assertBenchmarkFrontendResults(b, dst.OutcomeIDs, rows)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := scheduled.Execute(context.Background(), &dst, compiled, batch); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkFrontendBatch(b testing.TB, compiled *program.Program, rows uint32) eval.Batch {
	b.Helper()
	var builder eval.Builder
	if err := builder.Begin(compiled, rows, 0, 0); err != nil {
		b.Fatal(err)
	}
	blue, err := builder.InternSymbol([]byte("blue"))
	if err != nil {
		b.Fatal(err)
	}
	for row := uint32(0); row < rows; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			b.Fatal(err)
		}
		if err := builder.SetSymbol(row, 1, blue); err != nil {
			b.Fatal(err)
		}
		if err := builder.SetInteger(row, 2, 3); err != nil {
			b.Fatal(err)
		}
		if err := builder.SetBoolean(row, 3, true); err != nil {
			b.Fatal(err)
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		b.Fatal(err)
	}
	return batch
}

func assertBenchmarkFrontendResults(b testing.TB, outcomes []schema.OutcomeID, rows uint32) {
	b.Helper()
	if uint32(len(outcomes)) != rows {
		b.Fatalf("outcomes = %d, want %d", len(outcomes), rows)
	}
	for row := range outcomes {
		if outcomes[row] != 1 {
			b.Fatalf("outcome[%d] = %d, want Approve", row, outcomes[row])
		}
	}
}
