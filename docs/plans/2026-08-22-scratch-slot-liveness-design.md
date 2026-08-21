# Scratch-Slot Liveness Design

**Date:** 2026-08-22

**Status:** Approved

## Goal

Assign deterministic reusable scratch slots to the immutable instruction
schedule. Evaluation stores two truth bitplanes and sideband reason masks per
live result. Slot reuse must reduce peak batch scratch without changing
InstructionIDs, operands, source maps, or opcode runs.

Task 11 adds allocation metadata only. It does not add request batches,
applicability indexes, evaluator behavior, SIMD dispatch, or explanations.

## Program Model

`program.Program` gains four fields:

```go
TruthSlots     []schema.SlotID
ReasonSlots    []schema.SlotID
TruthSlotCount uint32
ReasonSlotCount uint32
```

The two slot columns are parallel to `Opcodes`. Slot zero means that an
instruction has no storage in that plane class. Truth results always have a
slot. Reason slots are assigned only to instructions in the reason-relevant
closure of semantic roots.

Every current leaf opcode can become unresolved: fact predicates can observe a
missing or invalid input, and evidence predicates can produce any engine
reason. All/Any/Not propagate those reasons. Consequently, every row in a
valid current Program is normally reason-relevant because Task 10 already
removed instructions outside semantic-root dependency cones. The separate
reason plan still makes the requirement explicit and permits future
reason-free opcodes without changing the evaluator layout.

Both columns are pointerless, one-based, and frozen with exact capacity. The
peak counts are the number of physical truth/reason slot bundles the evaluator
must provision, not the largest InstructionID.

## Liveness

Instruction operands remain InstructionIDs. Slot allocation is a separate
linear pass over the final topological schedule.

For each instruction, `lastUse` starts at its producer row. Every operand edge
extends the referenced instruction's last use to the consumer row. Any row
whose `RootFlags` contains applicability, assertion, or evidence is retained
through an end-of-program sentinel so execution and explanation can read it
after all runs complete.

The interval is closed for reads but permits one exact in-place handoff at the
final consumer. Immediately before assigning consumer row `i`, slots whose
last use is `i` become available. The consumer may therefore receive one of
its dying operand slots. This matches `truth.Not`, `truth.And`, and `truth.Or`,
which permit a destination to exactly alias a source. Variadic groups use a
deterministic dying operand as the in-place reduction driver when available;
Task 16 implements that choice.

No root slot is returned to the free set. A common-subexpression instruction
with several root roles still owns one retained slot.

## Deterministic Allocator

The compiler uses reusable typed scratch:

- one `lastUse` row per instruction;
- release-bucket heads and one next link per instruction;
- one free-slot bitset;
- one reason-relevance byte per instruction;
- temporary truth and reason slot columns.

Release buckets avoid a second scan of all previous instructions at each row.
The free bitset always selects the lowest available SlotID with
`bits.TrailingZeros64`. Releasing a lower word moves the free-word cursor back
to that word. When no bit is set, the allocator creates the next dense
one-based SlotID. The result is deterministic and linear in instructions,
operand edges, and touched bitset words.

All CSR arithmetic is widened before indexing or conversion. Malformed
parallel columns, invalid opcodes, invalid ranges, zero/forward operands, and
unknown root flags return `ErrInvalidGeneratedProgram`. A count that cannot be
represented by `SlotID` or the peak-count columns returns
`ErrProgramTooLarge`.

## Reason Plan

Reason relevance is computed without maps or recursion:

1. Seed all semantic roots.
2. Walk instructions in reverse topological order.
3. Mark every operand of a marked instruction.
4. Assign slots only to marked rows.

Current normalized Programs make this closure equal to all rows, but keeping
the pass explicit prevents a later reason-free instruction class from silently
receiving nine unnecessary reason planes.

Truth and reason slots use the same interval rules but separate free sets and
peak counts. They are not forced to share numeric IDs.

## Debug Mode

The internal planner accepts a retain-all mode. In this mode it never releases
slots: truth SlotID equals InstructionID, and reason-relevant rows receive
dense unique reason slots in instruction order. Every intermediate result then
remains available to deterministic debug execution.

The published Program stores the reuse plan. Task 36 may expose retain-all
planning through its debug API; Task 11 does not add a public mode or duplicate
debug columns in every Program.

## Compiler Integration And Ownership

Slot planning runs after Task 10 scheduling and before `program.Freeze`.
Planning writes only to the reusable compiler output Program. It does not
rewrite operands or reorder instructions.

`program.Freeze` copies the two slot columns into exact-capacity Program-owned
slices and copies the scalar peak counts. A failed plan or freeze leaves the
public destination unchanged under the existing atomic `Lowerer.Lower`
contract.

## Verification

Tests cover:

- a linear chain that repeatedly reuses one dying source slot;
- a branching DAG whose simultaneously live branches require separate slots;
- a shared node retained until its final consumer;
- semantic roots retained past the final instruction;
- several root roles merged onto one instruction;
- deterministic lowest-slot reuse;
- retain-all debug assignments;
- malformed and non-topological Program rejection without partial mutation;
- warm planner reuse without stale rows or allocation;
- exact frozen ownership and 386-safe conversions;
- interval verification proving that two simultaneously live values never
  share a slot.

Benchmarks report planner `ns/op`, `B/op`, and `allocs/op` after warm-up, plus
calculated peak evaluator bytes for reuse and retain-all plans:

```text
words = truth.WordCount(rows)
truth bytes  = words * 8 * 2 * TruthSlotCount
reason bytes = words * 8 * truth.ReasonCount * ReasonSlotCount
```

Planner warm reuse must allocate zero bytes. The benchmark fixture must show a
strict peak-byte reduction from liveness reuse; otherwise the allocator has not
demonstrated its purpose.
