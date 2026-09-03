# NornRune CLI Demo Performance Design

**Status:** Approved

**Date:** 2026-08-23

## Goal

Add one presentation-oriented `nornrune demo` command that runs the
complete embedded policy workflow in one process while keeping every existing
command's JSON contract unchanged.

## Measured Baseline

A trimmed built binary completes independent `evaluate`, `compile`, `explain`,
and `simulate` invocations in approximately 2-3 ms each on the development host.
The command must therefore reuse work within one process, but must not add a
daemon, disk cache, serialized Program, or other cross-process state.

## Command Contract

`nornrune demo` accepts the existing `--policy`, `--requests`, and `--evidence`
source flags and no positional arguments. It writes a plain-text report to
stdout and errors to stderr under the existing exit-code contract.

The report contains:

- policy name, version, SHA-256 hash, engine version, and compiled shape;
- the runtime-selected SIMD tier and description;
- all baseline request decisions and policy-authored rationales;
- an R3 standard-usage simulation showing the baseline and simulated outcome;
- an R2 aggregate-output simulation showing the baseline and simulated outcome;
- compile, decode, baseline evaluation, baseline rendering, simulation, and
  total in-process timings.

The text uses no ANSI escapes and is stable except for runtime diagnostics and
timings. Existing commands remain the machine-readable interface; the demo does
not add a second JSON result schema.

## Execution And Ownership

The command loads sources once, creates one CLI engine, compiles one immutable
Program, decodes one SoA batch, and evaluates the baseline once. It appends the
baseline report before subsequent evaluations reuse the engine-owned result
batch. The two simulations reuse one row selector and compact only R3 and R2
from the already-decoded source batch.

Policy outcomes and rationales are obtained from `result.Explainer`; no result
text or decision is hardcoded. Only the two bounded demonstration changes are
fixed inputs. Formatting appends into one byte slice and stdout is written once
after the complete report succeeds, so failures cannot expose partial output.

## Performance Boundaries

The demo adds no work to evaluator kernels. Compilation and decoding remain
once-per-command cold work; each scenario performs one bounded row compaction
and one one-row evaluation. The implementation records in-process stage
durations and adds a benchmark for the complete demo pipeline. Cross-process
caching is rejected because measured process invocations are already around
3 ms and cache I/O, invalidation, and artifact compatibility would add more
complexity than useful work.

## Tests

Tests cover command registration, complete baseline and scenario output,
policy-authored rationale text, external inputs, usage errors, pipeline errors,
stdout failure, deterministic timing formatting with an injected clock, and a
bounded full-pipeline benchmark. Task 26 then reruns native, purego, 386,
race/checkptr, vet, field-alignment, formatting, whitespace, and executable
golden-output gates.
