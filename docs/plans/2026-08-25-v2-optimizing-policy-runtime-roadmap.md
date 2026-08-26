# Potential V2 Optimizing Policy Runtime Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task, but only after the validation gate in this document is explicitly approved.

**Goal:** Preserve a possible path from the bounded v1 compatibility frontends to a full Cedar and CEL optimizing runtime, while deferring implementation until external workloads demonstrate demand.

**Architecture:** Keep the existing allocation-free scalar SIMD evaluator as the first execution tier. A possible v2 adds typed aggregate and entity storage, language-specific semantic IRs, shared typed SSA, fused portable kernels, and a columnar general CEL fallback; native JIT compilation remains an optional measured optimization, and full Rego remains a later query-engine project.

**Tech Stack:** Go 1.27, official CEL/Cedar/Rego parsers and conformance suites, existing NornRune SoA/CSR/bitplane kernels and fixed-worker scheduler, optional future LLVM ORC, Cranelift, or custom native backend.

---

**Status:** Deferred discovery roadmap, not an approved implementation commitment

**Date:** 2026-08-25

## Why This Exists

The v1 compatibility work deliberately accepts bounded CEL, Rego, and Cedar
subsets and lowers them into one existing NornRune evaluator. During Cedar
planning, we considered whether NornRune should eventually support complete
language semantics and compile them into a substantially broader optimizing
runtime.

That direction is technically possible, but it changes the product from a
small policy engine with compatibility adapters into a multi-language compiler
and runtime. It would require years rather than one frontend task. This
document records the opportunity, likely architecture, costs, and decision
gates so the current release does not absorb speculative scope.

Nothing in this document changes the approved v1 frontend plan. V1 remains a
bounded, explicit, differentially tested compatibility release.

## Product Hypothesis

The potential product is not another way to spell policies. Its proposed value
is:

> Compile existing policy expressions into allocation-free, indexed,
> SIMD-accelerated batch execution plans for high-throughput authorization and
> policy simulation.

Possible users include API gateways, multi-tenant SaaS authorization systems,
Kubernetes admission systems, data-access platforms, fraud and risk engines,
edge services, and organizations replaying policy changes over historical
requests.

The strongest initial wedge may be policy simulation rather than replacing a
live authorizer. Replaying millions of historical requests naturally benefits
from NornRune's batch layout, semantic diff, explanations, indexes, SIMD, and
fixed-worker scheduler. A useful product question is:

> If this policy version is deployed, which historical requests change
> decision, why, and where is required evidence missing?

Performance alone is insufficient. Adoption also requires semantic
compatibility, straightforward integration, deterministic diagnostics,
operational visibility, safe upgrades, and a migration path that does not
require rewriting an existing policy estate.

## Funding Reality

General repository sponsorship is unlikely before adoption. More plausible
early funding paths are:

- a design partner funding a feature needed by a real workload;
- paid integration or performance work;
- support contracts after production adoption;
- an employer funding upstream maintenance;
- a managed policy simulation or authorization service;
- a competitive ecosystem or foundation grant.

The v2 architecture must not begin merely because it is technically
interesting. It should begin when a real organization can identify a costly
authorization or simulation workload and provide enough detail to measure it.

## V1 Boundary

V1 finishes the existing policy-compatibility roadmap before any v2 runtime
work:

1. Complete bounded Cedar lowering through the existing semantic table.
2. Add deterministic Protobuf options and generated bindings.
3. Add explicit CLI frontend selection.
4. Lock cross-frontend conformance and official-engine differential tests.
5. Preserve evaluator dependency boundaries and zero-allocation warmed paths.
6. Complete fuzz, race, layout, benchmark, documentation, and release gates.
7. Describe every frontend as bounded and publish its exact capability matrix.

V1 does not add aggregate runtime values, a general VM, native code generation,
or full-language claims. Existing unsupported constructs continue to fail with
bounded deterministic diagnostics rather than approximate semantics.

## Semantic Cost By Language

### CEL

Complete CEL requires dynamic and typed values, lists, maps, Protobuf messages,
selection, indexing, optionals, conditionals, macros lowered to comprehensions,
overloaded functions, timestamps, durations, bytes, doubles, unsigned values,
null, and CEL-specific error and unknown propagation. The language is designed
to terminate, so typed compilation and specialization are practical.

Common scalar predicates and statically typed aggregate loops should compile
to fused bulk kernels. Dynamic values, custom host functions, and complex
variable-sized comprehensions need a complete general execution tier.

### Cedar

Complete Cedar requires entity UIDs, entity attributes, transitive hierarchy,
sets, records, tags, templates and slots, schema validation, extension values,
scope indexing, per-policy errors, and permit/forbid diagnostics. Its constrained
authorization model is a strong fit for static planning.

Most Cedar execution should compile to candidate-policy indexes, CSR entity
relationships, typed condition kernels, and permit/forbid bitsets. It should
not require a general query engine.

### Rego

Complete Rego requires packages, base and virtual documents, complete and
partial rules, unification, arrays, objects, sets, comprehensions, dynamic
references, multiple result bindings, negation-as-failure, document
construction, partial evaluation, rule indexing, and a large built-in surface.

Rego therefore needs a language-specific query planner and evaluator. A CEL
expression runtime is not sufficient. Rego can later reuse shared values,
arenas, indexes, scalar kernels, and scheduling, but it remains a separate
large project and is explicitly deferred.

## Candidate Architecture

```text
CEL frontend          Cedar frontend          Rego frontend later
     |                      |                         |
     +--------- language-specific typed HIR --------+
                            |
                     canonical typed SSA
                            |
       constant folding / specialization / CSE / pruning
       region formation / fusion / vector and parallel planning
                            |
       +--------------------+---------------------+
       |                    |                     |
existing scalar plan   fused typed plan   general language runtime
       |                    |                     |
SIMD bitplane kernels  aggregate/entity   columnar CEL instructions
                            |
                    optional native JIT
```

The canonical SSA must preserve language-specific states rather than flatten
them incorrectly. CEL error/unknown, Cedar per-policy error, Rego undefined,
and NornRune missing/stale/unclear/unverifiable/conflict are distinct semantic
concepts even when some ultimately produce the same product outcome.

## Execution Tiers

### Tier 1: Existing Scalar Kernels

The current SoA, CSR, truth-bitplane, SIMD, index, and fixed-worker paths remain
physically unchanged. Existing v1 programs stay on this tier and retain their
allocation and latency characteristics.

### Tier 2: Fused Aggregate And Entity Kernels

Typed loops operate over offset-based lists, sets, maps, records, entity
attributes, and hierarchy indexes. The compiler fuses compatible operations so
one traversal can evaluate several predicates without materializing every
intermediate result.

### Tier 3: Columnar General Execution

A compact typed instruction runtime supplies complete CEL semantics where a
fixed kernel is not available. It must dispatch by instruction over batches or
active row masks, never as a boxed stack interpreter inside every request.
Typed register slabs and worker-local arenas replace `any`, reflection, and
per-element heap allocation.

### Optional Tier 4: Native JIT

A future JIT may compile hot Tier 3 regions into native code to remove
instruction dispatch, fuse loops, and reduce intermediate memory traffic. It
is not required for v2 semantic completeness and is selected only after
profiling a complete portable runtime.

LLVM AOT is not a required milestone. If native compilation becomes useful,
LLVM ORC, Cranelift, and constrained template generation must be compared on
compile latency, generated-code quality, deployment complexity, binary size,
cacheability, portability, and runtime integration. LLVM can provide AOT and
JIT from one backend, but its dependency and C ABI costs are justified only by
measured hot general regions.

## Why Kernels Come Before A JIT

The current evaluator already compiles source into an immutable operation plan.
Its performance comes from bulk dispatch, contiguous typed data, bitplanes,
SIMD, pruning, and zero per-row allocation rather than policy-specific machine
code.

A native compiler adds little to a single memory-bound comparison or bitset
operation already implemented by a tuned kernel. Its best opportunity is a
complex expression that would otherwise require several instruction
dispatches, complete data passes, and intermediate bitplanes. Region fusion can
remove much of that cost without native code, so fusion must be measured first.

## Data And Runtime Foundations

A possible v2 requires:

- append-only typed scalar and aggregate value kinds;
- offset-based list, set, map, and record arenas grouped by lifetime;
- entity UID interning and CSR entity-parent relationships;
- typed entity-attribute columns and candidate-policy indexes;
- distinct presence, value, error, unknown, and language-state masks;
- capacity plans calculated at compile or batch-build time;
- worker-private scratch and one deterministic merge;
- stable kernel, portable-plan, persistence, and optional native-code ABI
  versions;
- instruction, collection, call, and external-effect budgets;
- no parser, reflection, map, or dynamic language object in existing evaluator
  kernels.

New layouts must not turn the current scalar columns into tagged unions. The
dynamic runtime is a sidecar used only by programs that require it.

## SIMD And Parallelism

SIMD and multithreading are selected where beneficial, not applied as labels to
every operation.

Strong SIMD candidates include numeric comparisons, Boolean bitplanes,
presence masks, symbol-ID equality, permit/forbid reduction, fixed-width
aggregate predicates, and set bitmaps. Poor candidates include small
collections, unpredictable hash probes, divergent dynamic control flow,
Unicode-heavy operations, variable-sized result construction, and external
calls.

Parallel work should shard contiguous request rows or existing policy/entity
boundaries, use worker-private outputs and scratch, avoid per-operation
barriers, and merge once. Small batches remain serial. Every parallel decision
needs a measured grain threshold and false-sharing review.

## Compatibility Contract

A promoted v2 plan must preserve:

- public v1 source and compile APIs unless a versioned alternative is added;
- append-only public and core enums and opcodes;
- existing scalar program and request layouts;
- v1 decisions, diagnostics, spans, and missing-data behavior;
- persisted-policy compatibility or an explicit deterministic migration;
- a direct v1 fast path with no general-runtime fields on its hot loop;
- portable fallback when optional native compilation fails;
- bit-identical outcomes between portable and native backends;
- exact capability claims for constructs not yet promoted to full support.

Before v2 implementation, v1 benchmark results, allocations, binary size,
startup cost, policy-install latency, and official differential corpora become
permanent regression baselines.

## Validation Gate

Do not promote this roadmap into an implementation plan until all required
conditions are met:

1. At least three external organizations describe a concrete authorization or
   policy-simulation performance problem.
2. At least one organization supplies an anonymized production-shaped policy
   and request workload or funds equivalent workload construction.
3. V1 demonstrates a material measured advantage or a clear missing capability
   on that workload.
4. At least one design partner commits engineering time, deployment testing,
   funding, or maintenance support.
5. Requested unsupported features cluster around one language strongly enough
   to choose Cedar or CEL first.
6. The expected benefit cannot be obtained by improving the official engine,
   contributing a smaller upstream optimization, or adding one bounded v1
   feature.

Useful evidence includes decisions per second, p50/p99 latency, CPU time,
memory, allocations, policy-install latency, candidate-pruning ratios, and time
to replay one million historical requests. Toy-expression wins do not satisfy
the gate.

## Candidate Milestones After Promotion

These milestones are ordered dependencies, not scheduled commitments.

### Milestone 1: Freeze V1 Baselines

Record representative single-request, batch, indexed, SIMD, and parallel
benchmarks; allocation profiles; persisted-format fixtures; and differential
corpora. Define a no-regression budget before changing runtime data structures.

### Milestone 2: Introduce The Typed Value ABI

Add aggregate and entity sidecars, reusable arenas, explicit language-state
masks, and versioned ABI definitions while preserving the scalar evaluator
unchanged. Test lifetime grouping, capacity reuse, poisoning, malformed offsets,
and zero-allocation warmed execution within planned capacity.

### Milestone 3: Add Typed HIR And SSA

Define language HIRs and one typed optimization representation. Implement
validation, ownership, exact source provenance, constant folding, dead-code
elimination, common-subexpression elimination, and deterministic serialization.

### Milestone 4: Build The Portable Fused Backend

Compile SSA regions into existing scalar kernels, new aggregate/entity kernels,
and typed columnar fallback instructions. Add cost-based fusion and serial versus
parallel scheduling without a native compiler dependency.

### Milestone 5: Promote One Full Language

Select Cedar or CEL from validation evidence. Reach official conformance before
claiming full support, then optimize representative external workloads. Cedar
is the likely technical first choice; CEL may be the product first choice if
design-partner demand is stronger.

### Milestone 6: Promote The Second Full Language

Reuse the proven value ABI, SSA, kernels, and runtime while preserving the
second language's distinct type and error semantics. Repeat conformance,
differential, fuzz, and workload gates.

### Milestone 7: Evaluate A Native JIT

Profile remaining Tier 3 costs. Prototype native compilation only for measured
hot regions, compare it with additional portable fusion, and retain it only if
end-to-end gains exceed compile, cache, binary, and operational costs.

### Milestone 8: Reconsider Rego

Design the rule/query engine only with demonstrated Rego demand and funding.
Reuse shared foundations but do not force unification and document semantics
through the CEL execution model.

## Decisions Explicitly Deferred

- Whether Cedar or CEL is the first complete language.
- Whether complete support includes impure and environment-dependent built-ins
  or a host-registration boundary.
- Whether Protobuf values are statically generated, descriptor-backed, or both.
- Whether the general runtime uses explicit bytecode, threaded regions, or
  generated specialized loops.
- Whether a native backend is needed at all.
- If needed, whether LLVM ORC, Cranelift, template JIT, or generated Go is the
  best backend.
- Whether native arenas are implemented in Go with bounded pinning or behind a
  C/Rust ABI.
- Which product integration is first: Go SDK, gRPC service, API gateway,
  Kubernetes admission, or policy-simulation workflow.
- Funding, governance, support, and release model.

## Estimated Scale

Order-of-magnitude estimates for production conformance and hardening are:

| Area | Estimated effort |
|---|---:|
| Typed values, SSA, arenas, and kernel ABI | 4-8 engineer-months |
| Full Cedar backend | 4-8 engineer-months |
| Full CEL backend | 5-10 engineer-months |
| Fused plan optimizer | 4-8 engineer-months |
| Optional native JIT and lifecycle | 4-8 engineer-months |
| Cross-platform SIMD expansion | 6-12 engineer-months |
| Parallel cost model and scheduling | 3-6 engineer-months |
| Conformance, fuzzing, and hardening | Continuous; at least 6-12 engineer-months |

Near-upstream parity across CEL, Cedar, and Rego is a multi-year compiler and
runtime program. These estimates are planning ranges, not promises.

## Immediate Decision

Defer this roadmap. Finish and release the bounded v1 compatibility frontends,
publish reproducible benchmarks and exact capability matrices, seek
production-shaped workloads and design partners, and revisit this document
only when the validation gate has evidence. Until then, optimize and extend v1
only through measured, bounded changes that preserve its current architecture.
