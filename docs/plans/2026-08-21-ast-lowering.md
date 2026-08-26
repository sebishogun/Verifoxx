# AST Normalization And Lowering Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Compile a validated pointerless AST into a deterministic immutable Program with canonical values, a normalized expression DAG, grouped topological instructions, stable source maps, and policy-defined resolution tables.

**Architecture:** A reusable `compile.Lowerer` builds typed scratch columns, structurally interns pure instruction candidates, flattens same-kind Boolean groups, removes dead normalized results, and emits a dependency-safe opcode-grouped schedule. The final `program.Program` owns exact-size SoA/CSR slabs, symbols, values, schema metadata, semantic roots, source spans, and a validated result Resolver; later tasks add liveness slots and indexes without rewriting instruction operands.

**Tech Stack:** Go 1.27, standard library, existing `internal/ast`, `internal/schema`, `internal/result`, and `internal/truth` packages

---

Read `docs/plans/2026-08-21-ast-lowering-design.md` before implementation.
Invoke `@superpowers:test-driven-development` before each production behavior
change. Every test, build, analyzer, and fuzz command below is bounded. Commit
only after the subtask passes specification and quality review.

The field-layout rule applies from the first declaration: put pointer-bearing
slice/table fields before fixed scalar metadata, keep hot parallel columns
together, and run the pinned production-only `fieldalignment` analyzer before
each production commit. Do not run its automatic fix blindly.

### Task 1: Define The Numeric Program Model

**Files:**
- Create: `internal/program/instruction.go`
- Create: `internal/program/program.go`
- Create: `internal/program/program_test.go`

**Step 1: Write failing opcode and frozen-symbol tests**

Test:

- `OpcodeInvalid` is zero and exactly twelve opcodes are valid: Equal,
  NotEqual, In, Exists, Less, LessEqual, Greater, GreaterEqual, Evidence, All,
  Any, Not.
- `RootFlags` can independently mark applicability, assertion, and evidence
  roots and combine them with `Has`.
- A manually constructed frozen symbol table returns exact bytes and one-based
  IDs for known entries, rejects ID zero/out-of-range, and returns false for an
  unknown byte sequence even when its FNV hash collides with a probed slot.
- `InstructionCount` returns the opcode-column length.
- Empty Program lookup is safe.

Use package `program`, not `program_test`, so malformed internal lookup-table
shapes can be tested without exporting construction helpers.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/program -run '^(TestOpcode|TestRootFlags|TestProgramSymbol|TestInstructionCount)'
```

Expected: FAIL because package `internal/program` and its types do not exist.

**Step 3: Implement instruction enums and Program columns**

Create `instruction.go` with:

```go
type Opcode uint8

const (
	OpcodeInvalid Opcode = iota
	OpcodeEqual
	OpcodeNotEqual
	OpcodeIn
	OpcodeExists
	OpcodeLess
	OpcodeLessEqual
	OpcodeGreater
	OpcodeGreaterEqual
	OpcodeEvidence
	OpcodeAll
	OpcodeAny
	OpcodeNot
)

type RootFlags uint8

const (
	RootApplicability RootFlags = 1 << iota
	RootAssertion
	RootEvidence
)
```

Add `Opcode.Valid`, `Opcode.IsGroup`, and `RootFlags.Has`. Keep zero invalid.

Create `program.go`. Declare every Program-owned column from the design,
including:

- Instruction, operand, literal-list, root, opcode-run, and source-map columns.
- Symbol bytes/ranges/hash slots plus explicit `ProgramSymbolCount`.
- Canonical value columns and packed integer/Boolean/timestamp payloads.
- Field name/kind/group columns and evidence-kind/state names.
- Requirement/clause roots, edges, satisfied/false outcomes, remediations, and
  every retained semantic/catalog source span.
- `result.OutcomeTable`, `result.RemediationTable`,
  `result.ResolutionTable`, and `result.Resolver` values.
- Input bytes followed by the fixed scalar tail: content hash, policy symbol
  IDs, and symbol count.

Methods:

```go
func (p *Program) InstructionCount() int
func (p *Program) Symbol(id schema.SymbolID) ([]byte, bool)
func (p *Program) LookupSymbol(value []byte) (schema.SymbolID, bool)
func (p *Program) ValueKind(id schema.ValueID) (schema.ValueKind, bool)
```

`LookupSymbol` hashes bytes through the shared `schema.HashSymbol` primitive,
probes power-of-two slot arrays, verifies stored hash, and compares slab bytes.
It never converts bytes to string or allocates. The shared primitive is the one
hash contract for interning, frozen lookup, and batch extension, so compiler
slot construction in Task 2 cannot drift from it. Reject mismatched slot
columns safely.

**Step 4: Verify GREEN and layout**

```bash
gofmt -w internal/program/instruction.go internal/program/program.go internal/program/program_test.go
go test -count=1 -timeout 60s ./internal/program
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/program
```

Expected: tests pass and fieldalignment prints nothing.

**Step 5: Review then commit**

```bash
git add internal/program
git commit -m "feat: define immutable program columns"
```

### Task 2: Canonicalize Program Symbols And Values

**Files:**
- Create: `internal/compile/normalize.go`
- Create: `internal/compile/lower.go`
- Create: `internal/compile/lower_test.go`

**Step 1: Write failing constant-lowering tests**

Add a `lowerFixture` helper that builds a field schema and source interner,
decodes `testdata/policies/valid-full.json`, validates it, and returns the
Document, Schema, and the same Interner that assigned field-name SymbolIDs.
The adapter package's fixture helpers are private, so construct this schema and
interner locally and require zero validation diagnostics before returning.

Test the private constant stage directly:

```go
var lowerer Lowerer
var got program.Program
err := lowerer.lowerConstants(&got, doc, fields, symbols)
```

Assert:

- Field IDs preserve row order while field names are translated into the
  Program symbol space with exact bytes, kinds, and groups.
- Program policy name/version, outcome names, evidence-kind names, and
  evidence-state names resolve through canonical Program SymbolIDs.
- Equal symbol bytes used by several AST ValueIDs map to one Program SymbolID.
- Duplicate typed literal payloads map to one Program ValueID; kinds remain
  distinct, so integer `1`, Boolean true, and timestamp `1` never alias.
- Program value payload refs are one-based for every kind.
- Frozen symbol slots are power-of-two and `LookupSymbol` finds field names,
  literals, metadata, outcomes, and evidence names.
- A frozen miss returns false and leaves `ProgramSymbolCount` unchanged,
  reserving all larger IDs for Task 13's batch extension table.
- Reusing the same Lowerer clears logical scratch while retaining capacity and
  produces identical canonical IDs.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^TestLowerConstants'
```

Expected: FAIL because Lowerer and `lowerConstants` do not exist.

**Step 3: Add bounded errors and typed interning scratch**

In `lower.go`, define static errors:

```go
ErrInvalidDocument
ErrInvalidSymbols
ErrEmptyPolicy
ErrProgramTooLarge
ErrInvalidGeneratedProgram
```

Define a non-concurrent `Lowerer` with reusable typed slices for:

- Validation diagnostics and Validator state.
- AST ValueID to Program ValueID and SymbolID remaps.
- Open-address symbol/value hash and ID slots.
- Later traversal, instruction, reachability, scheduling, and source-map state.

Order fields based on `fieldalignment`, but preserve hot scratch locality.

In `normalize.go`, hash symbol bytes through `schema.HashSymbol`, intern typed
values with exact collision checks, and append canonical symbols and typed
values into a scratch Program. Do not use `map`, `fmt`, reflection, interface
callbacks, or `[]byte`-to-string conversion.

`lowerConstants` must:

1. Verify each field name ID resolves in the provided source Interner.
2. Intern field bytes in FieldID order.
3. Walk AST ValueIDs in ascending order and canonicalize by kind/payload.
4. Translate metadata and catalog names through the ValueID remap.
5. Freeze an immutable symbol probe table sized to <= 50% occupancy.

The source Interner identity is a documented caller contract; this stage can
detect missing IDs but not a different valid interner with different bytes at
the same numeric ID.

**Step 4: Verify GREEN and no alignment regressions**

```bash
gofmt -w internal/compile/normalize.go internal/compile/lower.go internal/compile/lower_test.go
go test -count=1 -timeout 60s ./internal/compile -run '^TestLowerConstants'
go test -count=1 -timeout 60s ./internal/compile ./internal/program
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/program ./internal/compile
```

Expected: PASS and no production fieldalignment diagnostics.

**Step 5: Review then commit**

```bash
git add internal/compile/normalize.go internal/compile/lower.go internal/compile/lower_test.go
git commit -m "feat: canonicalize program constants"
```

### Task 3: Emit An Iterative Topological Instruction DAG

**Files:**
- Modify: `internal/compile/normalize.go`
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/lower_test.go`

**Step 1: Write failing opcode and topology tests**

Construct one valid document containing every compare op, Evidence, All, Any,
Not, a shared NodeID, and a deep Not chain. Test private
`lowerInstructions` after `lowerConstants`:

- Every AST operation maps to the exact Program opcode.
- Fields, scalar values, In-list values, evidence kind/state IDs, and ordered
  operand IDs land in the correct columns/ranges; unused columns are zero.
- Every operand InstructionID is nonzero and lower than its consumer.
- One shared source NodeID is emitted once even when reached through several
  parents.
- Canonical source NodeID/span columns are exact.
- An 8,192-node Not chain lowers without recursion or stack overflow.
- Repeated calls on one Lowerer do not leak prior visit state or operands.

At this stage do not test same-kind flattening or structurally equal distinct
source nodes; those are Task 4's RED cases.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^TestLowerInstructions'
```

Expected: FAIL because instruction lowering does not exist.

**Step 3: Implement iterative postorder emission**

Use fixed-width frames:

```go
type lowerFrame struct {
	node schema.NodeID
	next uint32
}
```

Before traversal, scan requirement and clause rows to OR root flags into one
byte per source node. Requirement applicability roots get
`RootApplicability`, clause assertion roots get `RootAssertion`, and clause
evidence nodes get `RootEvidence`.

Seed source NodeIDs in ascending order. Use white/gray/black byte state and an
explicit reusable stack. A frame completes only after all AST child NodeIDs are
black. Emit one temporary instruction per source NodeID, with children already
translated to temporary InstructionIDs.

Validated input is assumed by this private stage, but every widened
start+count/address conversion still returns `ErrInvalidDocument` rather than
panicking if an accessor unexpectedly fails.

Pre-size instruction columns from `doc.Len()` and edge columns from source CSR
lengths. No per-node allocation.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/compile/normalize.go internal/compile/lower.go internal/compile/lower_test.go
go test -count=1 -timeout 60s ./internal/compile -run '^TestLowerInstructions'
go test -count=1 -timeout 60s ./internal/compile ./internal/program
```

Expected: PASS.

**Step 5: Review then commit**

```bash
git add internal/compile/normalize.go internal/compile/lower.go internal/compile/lower_test.go
git commit -m "feat: emit topological policy instructions"
```

### Task 4: Flatten Groups, Deduplicate Expressions, And Remove Dead Results

**Files:**
- Modify: `internal/compile/normalize.go`
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/lower_test.go`

**Step 1: Write failing normalization tests**

Add focused tests for:

- `All(All(A,B),C)` becoming one live All instruction with ordered operands
  A,B,C; the non-root inner group result is removed.
- The corresponding Any case.
- A nested group that is also an applicability/assertion/evidence root retaining
  its own instruction for A,B while the parent uses A,B,C.
- The removed inner source node mapping to its ordered operand InstructionIDs;
  retained roots map to their own canonical instruction.
- Two distinct compare source nodes with equal field/op/canonical literal
  sharing one instruction.
- Duplicate In nodes sharing only when canonical ordered value lists match.
- Duplicate flattened groups sharing one instruction; operand-order differences
  remain distinct.
- RootFlags ORing when CSE merges roots with different roles.
- Exact live instruction and edge counts after compaction.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^TestNormalize'
```

Expected: FAIL because direct emission retains nested/duplicate work.

**Step 3: Add structural CSE and dead-result compaction**

For each completed source node, form a candidate over canonical Program IDs:

- Compare: opcode, field, scalar value or ordered list range.
- Evidence: kind and state.
- Not: one instruction operand.
- All/Any: recursively flattened same-kind child operands in source order.

Hash candidates into a pre-sized open-address table. Exact comparison includes
range contents. On a match, map the source node to the existing temporary
instruction and OR its RootFlags. Otherwise append one candidate row.

Retain each group's flattened operands in Lowerer scratch even when its own
result is CSE-merged. Mark semantic-root temporary IDs, scan live instructions
in reverse topological order, and mark operands. Compact only live temporary
instructions. Translate root marks and every per-node canonical/flat mapping
through the compaction remap before scheduling; discard CSE probe state once
candidate emission is complete.

Build source-map scratch:

- A source node whose canonical result survives maps to that one instruction.
- A non-root same-kind group whose result was removed maps to its remapped flat
  operand list.
- Any count/address above uint16/uint32 returns `ErrProgramTooLarge` before
  narrowing. This applies to instruction operand/list counts after flattening,
  source-map counts, and every CSR start/count conversion.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/compile/normalize.go internal/compile/lower.go internal/compile/lower_test.go
go test -count=1 -timeout 60s ./internal/compile -run '^TestNormalize'
go test -count=1 -timeout 60s ./internal/compile ./internal/program
```

Expected: PASS.

**Step 5: Review then commit**

```bash
git add internal/compile/normalize.go internal/compile/lower.go internal/compile/lower_test.go
git commit -m "feat: normalize policy expression DAGs"
```

### Task 5: Emit Deterministic Opcode-Grouped Topological Runs

**Files:**
- Modify: `internal/compile/normalize.go`
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/lower_test.go`

**Step 1: Write failing grouped-schedule tests**

Create a DAG where several leaf opcodes are initially ready, later logic nodes
unlock another run of an earlier opcode, and shared dependencies have multiple
users. Assert:

- The final schedule remains topological.
- When selecting the next run, lower Opcode wins among ready work; within an
  opcode, lower temporary canonical ID wins.
- Newly-ready instructions of the current opcode extend the same run.
- A dependency may force a later second run for one opcode.
- Run starts/counts are exact, contiguous, nonempty, cover every instruction
  exactly once, and match each instruction's opcode.
- Every instruction operand, semantic root, and source-map ID is remapped to
  final schedule IDs.
- Repeated lowering produces byte-identical instruction/run/source columns.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^TestGroupedSchedule'
```

Expected: FAIL because compacted postorder is not ready/opcode grouped and run
metadata is empty.

**Step 3: Implement the ready scheduler**

Build temporary user CSR and indegree columns from live operand edges. Use
typed ready state grouped by the fixed Opcode range; no `container/heap`
interface and no maps. Emit a run by selecting the lowest nonempty opcode and
the lowest temporary ID for that opcode. When an emitted instruction reduces a
user indegree to zero, mark that user ready. Continue the current opcode while
it has ready work, then close its run.

If no ready instruction exists before all live instructions are emitted,
return `ErrInvalidDocument`. Permute every per-instruction column into final
schedule order, then rewrite every ID-bearing cell once through the
old-to-final remap. Run rows are in final-ID order and need no later remap.

**Step 4: Verify GREEN and alignment**

```bash
gofmt -w internal/compile/normalize.go internal/compile/lower.go internal/compile/lower_test.go
go test -count=1 -timeout 60s ./internal/compile -run '^TestGroupedSchedule'
go test -count=1 -timeout 60s ./internal/compile ./internal/program
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/program ./internal/compile
```

Expected: PASS and no fieldalignment diagnostics.

**Step 5: Review then commit**

```bash
git add internal/compile/normalize.go internal/compile/lower.go internal/compile/lower_test.go
git commit -m "feat: group topological instruction runs"
```

### Task 6: Lower Semantic Roots And Resolution Tables

**Files:**
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/lower_test.go`
- Modify: `internal/program/program.go`

**Step 1: Write failing semantic/result-table tests**

On canonical and synthetic policies, assert:

- Requirement IDs/order, applicability InstructionIDs, clause CSR edges, and
  requirement spans are preserved.
- Clause assertion/evidence roots are translated to InstructionIDs; evidence,
  clause, outcome, remediation, and catalog spans are copied exactly.
- ClauseID `n` uses `result.RuleSetID(n)` and exactly nine rows.
- Missing/Stale/Unclear/Unverifiable/WrongScope/WrongSubject/WrongTiming/
  Invalid/Conflict map to the source columns fixed by the design.
- Satisfied and false outcomes remain separate clause columns.
- Source AST remediation kinds convert exactly to runtime kinds; fields,
  canonical Program ValueIDs, and evidence kinds remain typed.
- Each clause's remediation IDs are appended once. Nonterminal reason rows
  share that range; terminal rows have an empty range. Test synthetic
  Missing->Revise and Stale->Escalate behavior without branching on names in
  production.
- Empty aligned remediation tables remain valid.
- `result.NewResolver` accepts the generated tables, and Program's stored
  Resolver accessor returns the expected outcome/remediation rows.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^TestLowerSemantics'
```

Expected: FAIL because semantic and result tables are not populated.

**Step 3: Implement semantic lowering**

Copy requirement/clause/catalog rows in source row order. Translate every AST
ValueID through the canonical value remap and require symbol-valued names where
the validated contract says so.

Append one clause remediation edge range. For each reason row, select the
source OutcomeID, inspect its terminal column, and either share the clause
range or emit an empty range. Use widened arithmetic before every fixed-width
append.

Construct runtime tables over Program-owned slices, call `result.NewResolver`
once, and store the accepted Resolver. Add an exported read-only Program
accessor for that Resolver rather than exposing mutable compiler state. Convert
any constructor rejection to `ErrInvalidGeneratedProgram`.

Do not add Task 11 slots, Task 12 applicability indexes, or evaluator behavior.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/compile/lower.go internal/compile/lower_test.go internal/program/program.go
go test -count=1 -timeout 60s ./internal/compile -run '^TestLowerSemantics'
go test -count=1 -timeout 60s ./internal/compile ./internal/program ./internal/result
```

Expected: PASS.

**Step 5: Review then commit**

```bash
git add internal/compile/lower.go internal/compile/lower_test.go internal/program/program.go
git commit -m "feat: lower policy semantic tables"
```

### Task 7: Expose Atomic Validated Lowering And Freeze Exact Slabs

**Files:**
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/lower_test.go`
- Modify: `internal/program/program.go`

**Step 1: Write failing public-API and freeze tests**

Test both APIs:

```go
got, err := Lower(doc, fields, symbols)

var lowerer Lowerer
var dst program.Program
err = lowerer.Lower(&dst, doc, fields, symbols)
```

Assert:

- Nil destination/document/schema/interner and every validator-diagnosed
  malformed document return the bounded expected error without panic.
- No requirements or clauses returns `ErrEmptyPolicy`.
- Missing field SymbolIDs in the supplied interner returns
  `ErrInvalidSymbols`.
- Destination remains byte-for-byte unchanged after every error.
- A successful Program owns copied input/symbol/value/source arrays; resetting
  or mutating the AST builder and source interner after lowering does not
  change Program lookups or source bytes.
- Every final output slice has exact `len == cap` where it owns a frozen slab;
  empty slices remain nil unless a nonnil empty range is part of the contract.
- Two cold calls and repeated warm calls produce identical columns, canonical
  IDs, runs, source maps, roots, and result tables.
- Program element columns contain only numeric/value types and no pointers to
  AST nodes; use reflection only in tests.
- `ProgramSymbolCount + 1` cannot be returned by frozen lookup, pinning the
  batch-extension boundary.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^(TestLowerAPI|TestLowerErrors|TestLowerOwnership|TestLowerDeterministic|TestProgramPointerless)'
```

Expected: FAIL because public orchestration and atomic freeze behavior do not
exist.

**Step 3: Implement public orchestration**

`Lowerer.Lower` must:

1. Reject nil parameters before touching destination.
2. Run its reusable Validator and reject any diagnostics.
3. Reject empty requirement/clause policies.
4. Build constants, normalized instructions, grouped schedule, source maps,
   semantic roots, and result tables into local scratch/output.
5. Copy every owned output to exact-size slices.
6. Rebuild Program table slice headers and the Resolver over the frozen slices
   so no table still points at Lowerer scratch.
7. Assign `*dst = frozen` only after all stages succeed.

The convenience function uses a local Lowerer and Program. Lowerer scratch is
retained for reuse but is never borrowed by a returned Program.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/compile/lower.go internal/compile/lower_test.go internal/program/program.go
go test -count=1 -timeout 60s ./internal/compile -run '^(TestLowerAPI|TestLowerErrors|TestLowerOwnership|TestLowerDeterministic|TestProgramPointerless)'
go test -count=1 -timeout 60s ./internal/compile ./internal/program ./internal/result ./internal/truth
```

Expected: PASS.

**Step 5: Review then commit**

```bash
git add internal/compile/lower.go internal/compile/lower_test.go internal/program/program.go
git commit -m "feat: freeze compiled policy programs"
```

### Task 8: Run Final Verification And Cross-Cutting Review

**Files:**
- Modify only files required by confirmed review findings.

**Step 1: Add final boundary coverage**

Before verification, ensure tests cover:

- 8,192-deep iterative lowering.
- Maximum uint16 source-map/group counts at the accepted boundary and a bounded
  synthetic overflow rejection without allocating multi-gigabyte inputs.
- 32-bit ID conversion safety.
- Root group flattening, duplicate CSE, source maps, opcode runs, terminal
  remediation suppression, and exact Program ownership in one integration
  fixture.

Any uncovered behavior gets a new failing test before production changes.

**Step 2: Run focused and repository verification**

```bash
go test -count=1 -timeout 60s ./internal/program ./internal/compile ./internal/result ./internal/truth
go test -race -count=1 -timeout 120s ./internal/program ./internal/compile ./internal/result ./internal/truth
GOARCH=386 go test -count=1 -timeout 60s ./internal/program ./internal/compile ./internal/result ./internal/truth
go test -count=1 -timeout 60s ./...
timeout 120s go vet ./...
timeout 120s go build ./cmd/nornrune
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/program ./internal/compile
timeout 120s go build -gcflags=-m ./internal/program ./internal/compile
timeout 30s gofmt -l .
timeout 60s go mod tidy -diff
git diff --check
git status --short --branch
```

Expected: tests, race, 386, vet, and builds exit zero; fieldalignment, gofmt,
tidy diff, and diff check print nothing; status shows only the branch header.
Escape output may show intentional returned Program ownership, but no per-node
object escapes from normalization loops.

**Step 3: Request final Task 10 review**

Invoke `@superpowers:requesting-code-review` over the Task 10 commit range.
Review specifically for recursion, per-node allocation, maps/string conversion,
non-topological operands, unsafe CSE, root flattening semantic changes,
non-deterministic scheduling, stale scratch, Program borrowing compiler/AST
memory, source-map loss, symbol-space aliasing, malformed CSR narrowing,
resolution mapping errors, fieldalignment regressions, and accidental Task
11/12 scope.

Fix every confirmed Critical/Important issue with a new RED/GREEN cycle and
repeat Step 2.

**Step 4: Commit only review fixes when needed**

```bash
git add internal/program internal/compile docs/plans/2026-08-21-ast-lowering-design.md docs/plans/2026-08-21-ast-lowering.md
git commit -m "fix: harden AST lowering boundaries"
```
