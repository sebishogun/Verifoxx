# Policy-Authored Explanations Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Compile bounded policy-authored templates and lazily materialize deterministic decision explanations from correlated numeric provenance.

**Architecture:** Policy schema v1 gains mandatory root assumptions, resolution-owned rationale/uncertainty, and evidence-node issue templates. The AST parses templates once into pointerless operations, the compiler freezes immutable result tables while preserving source NodeIDs across CSE, the evaluator records correlated winning provenance, and `result.Explainer` renders one row into caller-owned reusable storage.

**Tech Stack:** Go 1.27, hand-written JSON scanner, pointerless SoA/CSR AST and Program layouts, four-valued evaluator, fixed-worker scheduler, atomic immutable Programs, append-based rendering.

---

All test, vet, build, benchmark, and analyzer commands below carry explicit
timeouts. Do not commit unless the user explicitly requests it.

### Task 1: Add Strong IDs And Parse Bounded Templates Once

**Files:**
- Modify: `internal/schema/id.go`
- Create: `internal/ast/template.go`
- Modify: `internal/ast/document.go`
- Modify: `internal/ast/builder.go`
- Test: `internal/ast/template_test.go`
- Modify: `internal/schema/schema_test.go`

**Step 1: Write the strong-ID and template parser tests**

Add compile-time namespace checks for `schema.TemplateID` and
`schema.ExplanationID`. Add table tests that call a new
`Builder.AddTemplate(text, context, span)` and require:

- literal-only text becomes one literal operation;
- every legal placeholder becomes its typed operation;
- `{{` and `}}` become literal braces;
- malformed braces, empty/unknown placeholders, and context-invalid bindings
  return `ErrInvalidTemplate` without changing any template column;
- 512 decoded bytes and 32 operations succeed, while the next value fails with
  `ErrTemplateTooLarge`; and
- reset/reuse retains capacities but clears active template state.

Use explicit expected operation and argument slices rather than comparing an
opaque object.

**Step 2: Run the RED gate**

```bash
go test -count=1 -timeout 60s ./internal/schema ./internal/ast -run 'Template|Namespaces'
```

Expected: FAIL because the IDs, template types, columns, and parser do not
exist.

**Step 3: Implement the pointerless parser and storage**

Add one-based IDs:

```go
type TemplateID uint32
type ExplanationID uint32
```

In `ast/template.go`, define hard bounds, a closed `TemplateContext`, and one
`TemplateOp` value for literal and each placeholder. Parse bytes in one pass,
append literal fragments into one slab, append operation/argument columns, and
record one header only after the parse succeeds. Use widened counts before
every fixed-width conversion. Keep temporary rollback lengths on the stack;
do not allocate a token slice.

Add these Document columns:

```go
TemplateBytes         []byte
TemplateOpStarts      []uint32
TemplateOpCounts      []uint16
TemplateLiteralStarts []uint32
TemplateMaxBytes      []uint32
TemplateContexts      []TemplateContext
TemplateOps           []TemplateOp
TemplateArgs          []uint32
```

Extend `Hints`, `NewBuilder`, and `Reset` for exact reusable storage.

**Step 4: Run the GREEN and 386 gates**

```bash
go test -count=1 -timeout 60s ./internal/schema ./internal/ast -run 'Template|Namespaces'
GOARCH=386 go test -count=1 -timeout 60s ./internal/ast -run '^TestTemplate'
```

Expected: PASS. Do not commit.

### Task 2: Model Assumptions, Decision Explanations, And Evidence Issues

**Files:**
- Create: `internal/ast/explanation.go`
- Modify: `internal/ast/document.go`
- Modify: `internal/ast/builder.go`
- Modify: `internal/ast/semantic.go`
- Test: `internal/ast/explanation_test.go`
- Modify: existing AST fixture helpers under `internal/ast/*_test.go`

**Step 1: Write failing builder and ownership tests**

Cover:

- `SetAssumptions` exactly once, including a valid empty list;
- `AddExplanation` with one rationale and zero to eight uncertainty templates;
- rejection of zero, out-of-range, wrong-context, oversized, and duplicate
  setup without partial mutation;
- `SetEvidenceIssueTemplates` requiring one TemplateID per engine reason and
  accepting a fallback-expanded nine-entry row;
- `Resolution` retaining seven nonzero ExplanationIDs beside its outcome IDs;
- accessors returning borrowed deterministic ranges; and
- reset/poison/reuse with no stale IDs.

**Step 2: Run the RED gate**

```bash
go test -count=1 -timeout 60s ./internal/ast -run '^Test(Explanation|Assumption|EvidenceIssue)'
```

Expected: FAIL because explanation storage and APIs do not exist.

**Step 3: Implement fixed and CSR columns**

Add:

```go
AssumptionTemplateIDs []schema.TemplateID

ExplanationRationaleIDs     []schema.TemplateID
ExplanationUncertaintyStarts []uint32
ExplanationUncertaintyCounts []uint16
ExplanationUncertaintyIDs    []schema.TemplateID

// Nine entries per Evidence payload row, fixed ReasonID order.
EvidenceIssueTemplateIDs []schema.TemplateID

// Seven entries per Clause row, fixed source resolution order.
ClauseExplanationIDs []schema.ExplanationID
```

Keep explanation text out of semantic value interning. Expand `ast.Resolution`
with a fixed seven-entry explanation array or equivalent named accessors while
retaining named outcome fields. Validate context compatibility in the builder.

**Step 4: Update AST fixture construction minimally**

Add one shared test helper that installs valid literal-only assumptions,
decision explanations, and evidence fallback templates. Use it in existing AST
tests rather than repeating nine reason entries in every test.

**Step 5: Run package tests**

```bash
go test -count=1 -timeout 60s ./internal/ast
```

Expected: PASS. Do not commit.

### Task 3: Decode The Strict Policy Schema V1 Explanation Shape

**Files:**
- Modify: `internal/adapters/jsonpolicy/decoder.go`
- Modify: `internal/adapters/jsonpolicy/expression.go`
- Modify: `internal/adapters/jsonpolicy/requirement.go`
- Modify: `internal/adapters/jsonpolicy/errors.go`
- Test: `internal/adapters/jsonpolicy/explanation_test.go`
- Modify: `internal/adapters/jsonpolicy/decoder_test.go`
- Modify: `internal/adapters/jsonpolicy/expression_test.go`
- Modify: `internal/adapters/jsonpolicy/requirement_test.go`

**Step 1: Add RED schema tests**

Build one minimal complete policy source and assert exact AST template,
assumption, evidence-issue, explanation, and clause-reference columns. Add
negative cases for:

- missing/duplicate/wrong-type `assumptions`;
- missing evidence `explanation` or fallback `issue`;
- unknown and duplicate evidence reason overrides;
- old string-valued resolution branches;
- missing `outcome`, `explanation`, `rationale`, or `uncertainty`;
- unknown/duplicate nested keys;
- malformed/context-invalid placeholders with exact source offsets;
- template, operation, list, and total hard limits; and
- arbitrary valid object-key order.

**Step 2: Run the RED gate**

```bash
go test -count=1 -timeout 60s ./internal/adapters/jsonpolicy -run 'Explanation|Assumption|Resolution'
```

Expected: FAIL because the decoder still accepts the old schema.

**Step 3: Extend reusable scanner state**

Add one required root key `assumptions`. Extend `Limits` with tighter optional
template/list controls bounded by the AST hard maxima. Add fixed local arrays
for nine issue templates and seven branch ExplanationIDs; do not allocate maps
or per-object slices.

Add `explanation` to the evidence expression key mask. Decode its required
fallback and optional overrides, then expand fallback IDs across all reasons
before applying overrides. Reject `{evidence_id}` and `{evidence_state}` in the
fallback and Missing contexts.

Decode each resolution value as:

```json
{
  "outcome": "Approve",
  "explanation": {
    "rationale": "...",
    "uncertainty": []
  }
}
```

Use source-order scratch with base/truncate discipline and reset it on every
success/error path.

**Step 4: Run focused and full adapter tests**

```bash
go test -count=1 -timeout 60s ./internal/adapters/jsonpolicy -run 'Explanation|Assumption|Resolution'
go test -count=1 -timeout 60s ./internal/adapters/jsonpolicy
```

Expected: focused tests PASS; full package may still fail only where old policy
fixtures require migration in Task 9. Do not commit.

### Task 4: Validate Every New AST Column And Reference

**Files:**
- Modify: `internal/compile/diagnostic.go`
- Modify: `internal/compile/validate.go`
- Test: `internal/compile/validate_explanation_test.go`
- Modify: `internal/compile/validate_corrupt_columns_test.go`
- Modify: `internal/compile/validate_semantic_structure_test.go`

**Step 1: Add RED corruption and semantic tests**

Clone one complete valid document and independently corrupt every new parallel
column, template operation/argument, literal range, context, template ID,
explanation CSR range, fixed evidence reason row, clause explanation row, and
assumption reference. Assert stable diagnostic table/member/row ordering and
that no malformed range panics on amd64 or 386.

**Step 2: Run the RED gate**

```bash
go test -count=1 -timeout 60s ./internal/compile -run 'Validate.*(Template|Explanation|Assumption)'
```

Expected: FAIL because validator coverage is absent.

**Step 3: Extend structural and semantic phases**

Add bounded diagnostic members for template, explanation, and issue tables.
Validate widened ranges before slicing. Require:

- equal template header column lengths;
- valid operation/context pairs and exact literal consumption;
- nonzero valid rationale and assumption TemplateIDs;
- valid uncertainty CSR ranges and references;
- exactly nine issue TemplateIDs per evidence row;
- exactly seven nonzero ExplanationIDs per clause; and
- maximum per-template and per-row expansion bounds.

Preserve existing deterministic table order and collect independent
diagnostics without reading through corrupt ranges.

**Step 4: Run native and 386 validator tests**

```bash
go test -count=1 -timeout 60s ./internal/compile -run 'Validate.*(Template|Explanation|Assumption)'
GOARCH=386 go test -count=1 -timeout 60s ./internal/compile -run 'Validate.*(Template|Explanation|Assumption)'
```

Expected: PASS. Do not commit.

### Task 5: Add Immutable Runtime Template And Explanation Tables

**Files:**
- Create: `internal/result/templates.go`
- Create: `internal/result/explanation_table.go`
- Modify: `internal/result/resolution.go`
- Test: `internal/result/templates_test.go`
- Modify: `internal/result/resolution_test.go`
- Modify: `internal/program/program.go`
- Modify: `internal/program/freeze.go`
- Test: `internal/program/freeze_test.go`

**Step 1: Write failing table validation and freeze tests**

Cover complete valid tables plus each malformed parallel length, operation,
range, ID, and expansion bound. Require `Resolver.Resolve` to return the
ExplanationID from the winning reason row with the existing outcome/reason
tie-break semantics. Require `Freeze` to exact-copy every new backing slice and
rebind all borrowed table views to the frozen owner.

**Step 2: Run the RED gate**

```bash
go test -count=1 -timeout 60s ./internal/result ./internal/program -run 'Template|Explanation|Freeze|Resolve'
```

Expected: FAIL because runtime tables and explanation IDs are absent.

**Step 3: Implement immutable non-owning tables**

Define result-package `TemplateOp`, `TemplateTable`, `ExplanationTable`, and
their allocation-free lookup/validation helpers. Extend `ResolutionTable` and
`Resolution` with ExplanationIDs. Add Program-owned columns for:

- template and explanation backing storage;
- assumption IDs;
- evidence issue IDs;
- requirement/clause source NodeIDs;
- clause branch ExplanationIDs; and
- resolution ExplanationIDs.

Order pointer-bearing fields before Program's fixed scalar tail. Update
`Freeze`, `ClearResultResolver`, and `ValidateResultTables` atomically.

**Step 4: Run result/program tests and field alignment**

```bash
go test -count=1 -timeout 60s ./internal/result ./internal/program -run 'Template|Explanation|Freeze|Resolve'
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/result ./internal/program
```

Expected: tests PASS; analyzer prints no actionable production finding. Do not
commit.

### Task 6: Lower Explanations While Preserving Source Nodes Across CSE

**Files:**
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/lower_semantics.go`
- Modify: `internal/compile/lower_instructions.go`
- Test: `internal/compile/lower_explanation_test.go`
- Modify: `internal/compile/lower_test.go`
- Modify: `internal/compile/lower_semantics_test.go`

**Step 1: Add RED lowering tests**

Require exact operation/argument/literal columns, exact explanation and issue
references, exact nine-row resolution expansion, and exact source NodeIDs for
requirements, assertions, and evidence. Build two semantically identical
evidence nodes with different issue wording and require one canonical
instruction but two preserved source nodes/templates. Poison reusable Lowerer
storage between calls and require no stale text or references.

**Step 2: Run the RED gate**

```bash
go test -count=1 -timeout 60s ./internal/compile -run 'Lower.*(Template|Explanation|SourceNode)'
```

Expected: FAIL because lowering ignores explanation columns.

**Step 3: Implement direct pointerless lowering**

Translate AST operations to result operations without rescanning source text.
Copy literal bytes and fixed/CSR columns with pre-sized reusable slices. Compute
actual maximum expansions from frozen symbol lengths and numeric ID widths;
reject generated output above hard bounds.

Retain source NodeIDs from requirement roots and clause edges before translating
them through `nodeCanon`. Expand the source `unverifiable` ExplanationID into
WrongScope, WrongSubject, WrongTiming, and Invalid resolution rows.

Validate result tables before `Freeze`, and leave destination unchanged on any
error.

**Step 4: Run compile/program tests and escape inspection**

```bash
go test -count=1 -timeout 60s ./internal/compile ./internal/program
timeout 60s go test -count=1 -run '^$' -gcflags=-m=2 ./internal/compile ./internal/program
```

Expected: tests PASS. Template/program storage may escape into the frozen
Program; no per-template temporary object should escape. Do not commit.

### Task 7: Record Correlated Winning Provenance Without Per-Row Allocation

**Files:**
- Modify: `internal/result/batch.go`
- Modify: `internal/eval/evidence_eval.go`
- Modify: `internal/eval/executor.go`
- Test: `internal/eval/explanation_provenance_test.go`
- Modify: `internal/eval/executor_test.go`
- Modify: `internal/eval/executor_bench_test.go`

**Step 1: Add RED provenance tests**

Cover satisfied, false, every reason, multiple simultaneous reasons, missing
evidence, state-derived reasons, qualifier mismatch, explicit conflict state,
aggregate positive-plus-negative conflict, fact-derived Missing, and duplicate
evidence records. Require exact:

- DriverExplanationIDs;
- EvidenceIDs in request edge order;
- ReasonIDs in ascending order;
- source ReasonNodes rather than canonical first-source nodes;
- first causal ReasonEvidenceID/state in edge order; and
- zero evidence ID/state for Missing and fact-derived reasons.

Add poisoned destination reuse and warm `AllocsPerRun` coverage.

**Step 2: Run the RED gate**

```bash
go test -count=1 -timeout 60s ./internal/eval -run 'Explanation|Provenance'
```

Expected: FAIL because result columns and causal provenance are absent.

**Step 3: Refactor one shared evidence-record classifier**

Extract an inlineable allocation-free classifier used by both the bulk evidence
loop and selected-row provenance scan. Keep aggregate truth/reason behavior
byte-for-byte equivalent to existing tests. Do not allocate arrays or invoke a
callback per record.

**Step 4: Extend result capacity and candidate reduction**

Add `DriverExplanations`, `ReasonNodes`, `ReasonEvidenceIDs`, and
`ReasonEvidenceStates` to `result.Batch`, reset logic, and capacity planning.
Carry ExplanationID and source nodes through `outcomeCandidate`. After the
winner is known, append correlated reason provenance and request EvidenceIDs.
Use widened maximum-edge arithmetic before mutating `dst`.

**Step 5: Run evaluator tests and allocation benchmark**

```bash
go test -count=1 -timeout 60s ./internal/eval
go test -count=1 -timeout 120s -run '^$' -bench='Executor' -benchmem ./internal/eval
```

Expected: PASS; warmed execution remains at zero allocations. Do not commit.

### Task 8: Merge New Provenance Through The Fixed-Worker Scheduler

**Files:**
- Modify: `internal/scheduler/shard.go`
- Modify: `internal/scheduler/arena.go`
- Test: `internal/scheduler/explanation_provenance_test.go`
- Modify: `internal/scheduler/scheduler_test.go`
- Modify: `internal/scheduler/arena_test.go`

**Step 1: Add RED shard validation and parity tests**

Require malformed parallel driver/reason columns to fail before destination
mutation. Compare serial and scheduler output for every new column over 0, 1,
63, 64, 65, 256, and uneven final-shard rows. Poison worker/private/destination
storage before reuse.

**Step 2: Run the RED gate**

```bash
go test -count=1 -timeout 60s ./internal/scheduler -run 'Explanation|Provenance|Merge'
```

Expected: FAIL because merge omits the new columns.

**Step 3: Extend validate-count-grow-copy merge**

Validate every parallel length against DriverOffsets or ReasonOffsets, sum
edges with widened arithmetic, grow destination once, and append private shard
columns in shard order. Update arena reset/poison checks and preserve zero warm
allocations.

**Step 4: Run scheduler race and benchmark gates**

```bash
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/scheduler
go test -count=1 -timeout 120s -run '^$' -bench='Scheduler' -benchmem ./internal/scheduler
```

Expected: PASS with zero warm allocations at established benchmark shapes. Do
not commit.

### Task 9: Materialize Bounded Explanations Into Caller Storage

**Files:**
- Create: `internal/result/explain.go`
- Test: `internal/result/explain_test.go`
- Benchmark: `internal/result/explain_bench_test.go`
- Modify: `internal/program/program.go`
- Test: `internal/program/program_test.go`

**Step 1: Add RED materialization acceptance tests**

Build a complete immutable catalog and result row. Assert exact rationale,
applied requirement view, used evidence view, evidence issue ordering,
assumptions, unresolved uncertainty, and both remediation kinds. Cover every
placeholder, literal brace escapes, maximum decimal IDs, symbol/value kinds,
missing evidence without an invented ID, and nonmissing evidence with ID/state.

Add malformed catalog/result/range/ID tests, maximum output tests, failed-call
atomicity, poison/reuse, nil receiver/destination, deterministic repeated
output, and exact zero warm allocations with pre-sized storage.

**Step 2: Run the RED gate**

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestExplainer'
```

Expected: FAIL because Explainer and materialized types do not exist.

**Step 3: Implement catalog binding and append rendering**

Add static sentinels `ErrInvalidExplanationCatalog`,
`ErrInvalidExplanationResult`, and `ErrExplanationTooLarge`. `Bind` validates a
borrowed catalog into temporary state and commits only on success.

`Materialize` validates the complete row first, resets active destination
lengths only after validation, and appends operations directly. Use
`strconv.AppendUint/AppendInt/AppendBool` or equivalent allocation-free append
helpers, never `fmt` or byte-to-string conversion. Resolve Program symbols and
typed remediation values through borrowed numeric columns. Keep all text in one
byte slab and store uint32 ranges.

Expose `Program.ExplanationCatalog()` as a read-only result-package view without
copying any backing storage.

**Step 4: Run materializer, 386, and benchmark gates**

```bash
go test -count=1 -timeout 60s ./internal/result ./internal/program -run 'Explainer|ExplanationCatalog'
GOARCH=386 go test -count=1 -timeout 60s ./internal/result -run '^TestExplainer'
go test -count=1 -timeout 120s -run '^$' -bench='Explainer' -benchmem ./internal/result
```

Expected: PASS and zero allocations/op for the warmed pre-sized benchmark. Do
not commit.

### Task 10: Migrate Policies, Fixtures, And Conformance Projection

**Files:**
- Modify: `policies/nornrune/policy.json`
- Modify: `testdata/policies/*.json`
- Modify: policy source helpers under `internal/fixtures`
- Modify: JSON policy fixtures embedded in package tests
- Modify: `internal/conformance/nornrune_test.go`
- Modify: `testdata/golden/requests.json`
- Modify: `results/requests.json`

**Step 1: Add policy-authored text to every valid policy**

Keep schema version 1. Add required assumptions, evidence issue fallback and
overrides, and seven resolution objects per clause. Use concise reusable test
text in generic fixtures and the current authored rationale/uncertainty text in
the NornRune policy.

Do not invent an EvidenceID for Missing. Update those two golden evidence issue
lines to name the required evidence kind. Retain E4 for the actual conflicting
record.

**Step 2: Replace hardcoded conformance prose**

Delete `semanticDetails`. Bind one Explainer to the compiled Program, reuse one
Materialized destination across rows, and project its ranges and structured
remediation into the temporary golden-test structs. Decisions continue to come
only from evaluator output.

**Step 3: Run adapter-to-conformance tests**

```bash
go test -count=1 -timeout 60s ./internal/adapters/jsonpolicy ./internal/compile ./internal/conformance
```

Expected: PASS with policy-authored text and updated content hash.

**Step 4: Run the full repository test once**

```bash
go test -count=1 -timeout 60s ./...
```

Expected: PASS. Do not commit.

### Task 11: Run Static, Architecture, Review, And Final Gates

**Files:**
- Verify: all Task 24 production, fixture, test, benchmark, and design files
- Modify: `docs/performance.md` with measured explanation benchmark only

**Step 1: Format and run static/layout checks independently**

```bash
timeout 30s gofmt -w internal/schema internal/ast internal/adapters/jsonpolicy internal/compile internal/result internal/program internal/eval internal/scheduler internal/conformance
timeout 60s go vet ./internal/schema ./internal/ast ./internal/adapters/jsonpolicy ./internal/compile ./internal/result ./internal/program ./internal/eval ./internal/scheduler ./internal/conformance
timeout 60s env GOARCH=386 go vet ./internal/schema ./internal/ast ./internal/adapters/jsonpolicy ./internal/compile ./internal/result ./internal/program ./internal/eval ./internal/scheduler ./internal/conformance
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/ast ./internal/result ./internal/program ./internal/eval ./internal/scheduler
```

Expected: all exit zero; review each layout suggestion against access locality
and GC scan bytes before editing.

**Step 2: Inspect architecture and escape output**

Confirm evaluator kernels contain no string conversion, template parsing, map,
reflection, or per-row allocation. Confirm template operations are immutable,
source nodes survive CSE, every result parallel column is validated before
slicing, and callbacks do not enter rendering. Inspect bounded package-specific
`-gcflags=-m=2`; caller-owned warmed paths must not allocate.

**Step 3: Request separate specification and code-quality reviews**

Review against roadmap Task 24, the approved architecture, and
`docs/plans/2026-08-22-policy-authored-explanations-design.md`. Fix every
Critical and Important finding, add a RED regression for each behavior defect,
and rerun affected bounded commands.

**Step 4: Run final bounded gates**

```bash
go test -count=1 -timeout 60s ./internal/result ./internal/program ./internal/eval ./internal/scheduler ./internal/conformance
GOARCH=386 go test -count=1 -timeout 60s ./internal/result ./internal/program ./internal/eval ./internal/scheduler ./internal/conformance
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/result ./internal/program ./internal/eval ./internal/scheduler ./internal/conformance
go test -count=1 -timeout 60s ./...
go test -count=1 -tags=purego -timeout 60s ./...
timeout 30s gofmt -l .
git diff --check
```

Expected: all tests pass; formatting and whitespace checks print nothing. Do
not commit; the roadmap commit message, if later requested, is
`feat: compile policy-authored explanations`.
