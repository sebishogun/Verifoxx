# NornRune CLI Demo Performance Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a comprehensive plain-text `nornrune demo` command that compiles and decodes once, presents all baseline decisions, runs two bounded simulations, and reports the selected SIMD tier and in-process timings.

**Architecture:** A demo runner composes the existing CLI engine, row selector, typed override parser, and policy-authored result explainer. It appends one report in memory and writes stdout once; existing JSON commands and evaluator kernels remain unchanged.

**Tech Stack:** Go 1.27, Cobra v1.10.2, immutable Program, SoA request batches, SIMD runtime dispatch, caller-owned result storage, append-based text formatting.

**Repository Rule:** Do not create commits unless the user explicitly requests them. The commit checkpoints below are optional only.

---

### Task 1: Lock The Demo Command Contract

**Files:**
- Create: `internal/adapters/cli/demo_test.go`
- Modify: `internal/adapters/cli/root.go:178-184`

**Step 1: Write the failing command tests**

Add tests that run `demo` through `executeWithDependencies` and assert:

- exit code 0 and empty stderr;
- policy metadata, engine version, fixed SIMD diagnostics, and compiled counts;
- all five baseline request IDs, outcomes, and policy-authored rationales;
- `R3 Revise -> Approve` for `environment.usage=standard`;
- `R2 Reject -> Approve` for aggregate analysis and aggregate counts;
- compile, decode, evaluate, both simulation, and total timing lines;
- the command accepts all three external source flags;
- positional arguments are usage errors;
- malformed input and stdout failure return status 1 without partial stdout.

Use a package-local `demoRuntime` value and monotonically increasing fake clock
so exact timing text is host-independent.

**Step 2: Run the focused tests and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run '^TestDemo' ./internal/adapters/cli
```

Expected: FAIL because `demo` is not registered and the demo runner does not
exist.

**Step 3: Register the command skeleton**

Add `newDemoCommand(deps)` to the root command list. The command must use
`cobra.NoArgs`, bind `sourceAll`, and delegate all execution and formatting to
the runner added in Task 2.

**Step 4: Run the root registration test**

Run the focused command above. Expected: tests progress past unknown-command
handling and continue failing on missing report behavior.

### Task 2: Implement One-Pass Demo Execution

**Files:**
- Create: `internal/adapters/cli/demo.go`
- Modify: `internal/adapters/cli/demo_test.go`

**Step 1: Define runner state and timing input**

Keep state local to one command:

```go
type demoRunner struct {
    engine       engine
    selector     rowSelector
    explainer    result.Explainer
    materialized result.Materialized
    output       []byte
}

type demoTimings struct {
    compile  time.Duration
    decode   time.Duration
    evaluate time.Duration
    revise   time.Duration
    aggregate time.Duration
    total    time.Duration
}
```

Order fields after implementation using the field-alignment analyzer. Pass a
`func() time.Time` and `simdops.RuntimeInfo` to the internal runner; production
uses `time.Now` and `simdops.Runtime()`, while tests provide deterministic
values.

**Step 2: Compile, decode, and evaluate exactly once**

The runner must:

1. Start the command clock.
2. Call `engine.compilePolicy` once.
3. Call `engine.decodeBatch` once.
4. Call `engine.evaluate` once for the baseline.
5. Bind one `result.Explainer` to `compiled.ExplanationCatalog()`.
6. Append policy/runtime metadata and all baseline rows before reusing results.

Resolve every outcome and rationale through immutable Program metadata and
`Explainer.Materialize`. Return an operational pipeline error for malformed
metadata or results.

**Step 3: Reuse the decoded batch for both scenarios**

Find request rows by strongly typed `schema.RequestID`. Store the two baseline
OutcomeIDs as scalar values before result storage is reused. Parse the fixed
overrides through `parseOverrides` into stack-backed storage, then call
`compactWithOverrides` and `engine.evaluate` for each scenario:

```text
R3: environment.usage=standard
R2: action.type=aggregate_analysis
    action.output=aggregate_counts
```

Append actual policy-derived before/after outcome names and simulated rationale
text. Missing R2 or R3 is an operational demo-input error.

**Step 4: Append deterministic text and timings**

Use `append`, `strconv.AppendUint`, and `strconv.AppendInt`; do not use maps,
reflection, or `fmt`. Render durations as integer microseconds with ASCII `us`.
Write stdout once through `writeComplete` only after every stage succeeds.

**Step 5: Run focused tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run '^TestDemo' ./internal/adapters/cli
```

Expected: PASS.

### Task 3: Measure The Complete Pipeline

**Files:**
- Create: `internal/adapters/cli/demo_bench_test.go`
- Modify: `docs/performance.md`

**Step 1: Add cold command benchmarks**

Add `BenchmarkDemoPipeline`, `BenchmarkDemoCommand`, and
`BenchmarkEvaluateCommand` using embedded sources and `io.Discard`. The
pipeline benchmark uses fixed runtime diagnostics and a monotonic no-op clock;
the command benchmarks include Cobra construction and dispatch. Report
allocations.

**Step 2: Run the benchmarks once**

Run:

```bash
timeout 180s go test -timeout 120s -run '^$' -bench='Benchmark(DemoPipeline|DemoCommand|EvaluateCommand)$' -benchmem ./internal/adapters/cli
```

Expected: both benchmarks complete; record ns/op, B/op, and allocs/op.

**Step 3: Rebuild and measure process latency**

Run:

```bash
timeout 120s go build -trimpath -o /tmp/opencode/nornrune-task26 ./cmd/nornrune
bash -c 'TIMEFORMAT="elapsed=%R user=%U sys=%S"; time /tmp/opencode/nornrune-task26 demo >/dev/null'
```

Expected: successful demo execution in the same millisecond order of magnitude
as the measured 2-3 ms standalone commands.

**Step 4: Record measured evidence**

Add the built-binary and benchmark results to `docs/performance.md`. Do not
claim a speedup without comparable before/after measurements.

### Task 4: Complete Task 26 Gates

**Files:**
- Modify only files identified by gate failures.

**Step 1: Run focused and full tests**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/cli ./internal/app ./cmd/nornrune
timeout 180s go test -count=1 -timeout 120s ./...
timeout 180s go test -count=1 -timeout 120s -tags=purego ./...
timeout 180s env GOARCH=386 go test -count=1 -timeout 120s ./...
timeout 240s go test -count=1 -timeout 180s -race -gcflags=all=-d=checkptr=2 ./...
```

Expected: PASS.

**Step 2: Run static and layout gates**

```bash
timeout 120s go vet ./...
timeout 120s env GOARCH=386 go vet ./...
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/adapters/cli
test -z "$(gofmt -l .)"
git diff --check
```

Expected: no diagnostics or listed files.

**Step 3: Recheck the machine-readable golden contract**

```bash
timeout 120s go run ./cmd/nornrune evaluate > /tmp/opencode/task26-results.json
cmp /tmp/opencode/task26-results.json results/requests.json
```

Expected: byte-identical output.

**Step 4: Inspect the presentation command**

```bash
timeout 120s go run ./cmd/nornrune demo
```

Expected: one complete plain-text report with five baseline decisions, two
simulations, runtime SIMD diagnostics, and timings.

**Step 5: Optional commit checkpoint**

If the user explicitly requests a commit:

```bash
git add docs/plans/2026-08-23-cli-demo-performance-design.md docs/plans/2026-08-23-cli-demo-performance.md internal/adapters/cli/demo.go internal/adapters/cli/demo_test.go internal/adapters/cli/demo_bench_test.go internal/adapters/cli/root.go docs/performance.md
git commit -m "feat: add comprehensive policy demo"
```
