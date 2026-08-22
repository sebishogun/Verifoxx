# Program Execution And Outcome Resolution Design

**Date:** 2026-08-22

**Status:** Approved

## Goal

Task 16 executes one immutable `program.Program` over one columnar request
batch, composes the four-valued instruction DAG in compiler-assigned scratch
slots, and reduces all relevant clauses to one deterministic policy-owned
outcome per request. The warm path must allocate nothing and must leave enough
numeric provenance for later conformance and explanation work.

SIMD dispatch, row sharding, textual explanations, and service ownership remain
later tasks. This task establishes the scalar reference executor they compare
against.

## Alternatives

Three execution shapes were considered:

1. Retain truth and reason planes for every instruction. This is simple, but it
   discards the compiler's liveness plan and scales scratch with instruction
   count instead of peak live values.
2. Interpret the entire Program once per request row. This uses little scratch,
   but turns every instruction into a per-row branch and prevents whole-column
   SIMD and natural row sharding.
3. Execute the topological schedule over reusable liveness slots. Leaves and
   Boolean operations process complete bitplanes, roots remain available for
   resolution, and scratch scales with `TruthSlotCount` and `ReasonSlotCount`.
   This is the selected design.

## API And Ownership

`eval.Executor` is a reusable, mutable execution context:

```go
type Executor struct {
    // Program binding and reusable pointerless scratch.
}

func (e *Executor) Execute(dst *result.Batch, p *program.Program, src Batch) error
```

The zero value is usable. An Executor is owned by one caller or worker and is
not safe for concurrent use. The Program and input Batch are borrowed and must
remain immutable for the call. `dst` owns its result storage and may be reused
on the next call. A successful call replaces its active lengths; no output may
outlive the storage owner that supplied it.

Program binding classifies evidence states and binds a reusable applicability
query once per Program pointer. Binding and all shape/width checks occur before
`dst` is changed. Malformed input returns a bounded sentinel error. Generated
Program invariants that become impossible after successful compiler validation
remain static-string panics in the package-private kernels.

## Scratch Layout

Truth scratch is one flat `[]uint64` slab. Each one-based truth slot occupies
two adjacent row planes, positive then negative:

```text
truth slot bytes = 2 * truth.WordCount(rows) * 8
```

Reason scratch is a second flat slab. Each one-based reason slot contains nine
reason-major row planes in fixed `truth.ReasonID` order:

```text
reason slot bytes = truth.ReasonCount * truth.WordCount(rows) * 8
```

All size arithmetic is widened before conversion. Slice views are stack-only
headers. Slots are overwritten completely before reading, so shrinking and
regrowing within retained capacity does not require clearing the whole arena.
Tail bits are masked by the existing truth and leaf kernels.

Additional Executor scratch stores one requirement-candidate mask, indexed
selector values/presence, selected remediation ranges, and temporary result
counts. These are typed contiguous slices, never per-row objects.

## Schedule Execution

Instructions execute once in ascending `InstructionID` order. Operand IDs are
already topological.

- Comparison and presence rows call the Task 15 scalar predicate kernel.
- Evidence rows call an unchecked reducer after the input evidence columns and
  CSR have been validated once for the whole execution. The public-in-package
  Task 15 wrapper retains full validation for direct tests.
- `All` reduces operand truth with `truth.And`; `Any` uses `truth.Or`; `Not`
  uses `truth.Not`.
- Boolean reason output is the bitwise union of operand reason planes. `Not`
  preserves reasons unchanged. Reasons are consumed only when the resulting
  semantic root is unresolved or conflicting, so a reason on a branch dominated
  by a definite true/false result cannot change that definite outcome.

The compiler may assign a group destination to any operand slot that dies at
that consumer. Before reducing a variadic group, the executor selects the first
operand in source order whose slot equals the destination as the in-place
driver. If none aliases, it copies the first operand. It then reduces every
other operand in source order. Truth and reason slots select their drivers
independently because their liveness plans are separate. This avoids
overwriting a later operand before it is read and makes execution deterministic.

## Applicability And Clauses

For each request row, a bound query over `Program.ApplicabilityIndex` produces
a conservative requirement-row candidate mask from present symbolic selector
facts. Static index shape is validated at bind time, not once per row. Missing
selectors retain candidates. A requirement excluded by the index is definitely
inactive and is not resolved.

Candidate requirements are visited in Program row order:

- Definite false applicability is inactive and emits no candidate outcome.
- Definite true applicability records the requirement as applied and evaluates
  each referenced clause in CSR source order.
- Unknown or conflicting applicability records the requirement as relevant and
  resolves its reason mask through each referenced clause's rule set. Clause
  assertions are not treated as violations while applicability itself is
  unresolved.

An active clause is the four-valued conjunction of its assertion root and all
of its evidence roots. The roots are combined directly for the selected row;
no extra scratch slot is needed. A definite true clause proposes
`ClauseOnSatisfied`, a definite false clause proposes `ClauseOnFalse`, and an
unknown/conflicting clause calls `Resolver.Resolve` with the union of its root
reason masks. `RuleSetID == ClauseID` remains the lookup invariant.

An unresolved semantic root with no reason is an execution invariant failure;
the engine must not invent Missing or silently drop the clause.

## Outcome And Driver Reduction

Every clause candidate enters the same policy-owned precedence reduction:

1. Higher numeric outcome precedence wins.
2. Equal precedence selects the lower `OutcomeID`.
3. If the selected OutcomeID is unchanged, the first candidate encountered in
   requirement and clause source order remains the driver.
4. Within one unresolved candidate, `Resolver.Resolve` supplies its stable
   driver reason.

The selected driver stores its requirement, clause, canonical source node, and
optional reason IDs. A false or unresolved nonterminal winner carries the
bounded clause/remediation range; terminal winners carry none. A batch row with
no relevant requirement has outcome zero and no driver, representing no policy
coverage rather than one of the policy pack's outcomes.

## Result Batch

`result.Batch` is caller-owned SoA/CSR storage. Fixed result columns are indexed
by request row; variable provenance uses offsets of length `Rows+1`:

```go
type Batch struct {
    OutcomeIDs []schema.OutcomeID

    RequirementOffsets []uint32
    RequirementIDs     []schema.RequirementID

    DriverOffsets        []uint32
    DriverRequirements   []schema.RequirementID
    DriverClauses        []schema.ClauseID
    DriverNodes          []schema.NodeID
    DriverReasons        []schema.ReasonID

    EvidenceOffsets []uint32
    EvidenceIDs     []schema.EvidenceID
    ReasonOffsets   []uint32
    ReasonIDs       []schema.ReasonID

    RemediationOffsets []uint32
    RemediationIDs     []schema.RemediationID

    Rows uint32
}
```

Task 16 fills all relevant requirement IDs, the one deterministic winning
driver, every active reason on that winning candidate in ascending reason
order, and its remediation IDs. Evidence provenance columns are initialized to
valid empty ranges; Task 24 will materialize used-evidence provenance without
changing the batch shape. Adapters receive IDs only and do not enter the
execution kernel.

All edge counts and offsets are checked against `uint32` and `int` before
writing. Fixed and bounded-maximum columns are sized before row work; actual
edge slices are compacted to their written lengths. Repeating an equal-or-
smaller execution with the same Executor and result Batch allocates zero bytes.

## Verification

Tests cover:

- applicable, inactive, satisfied, false, unknown, and conflicting paths;
- unresolved applicability remaining unresolved instead of becoming false;
- assertion plus multiple evidence roots as one four-valued conjunction;
- simultaneous clause/requirement outcomes, precedence, lower-ID ties, and
  first-driver stability;
- variadic groups whose destination aliases the first, middle, last, or no
  operand slot, including independent reason-slot aliasing;
- all nine reasons, reason unions, terminal remediation suppression, and
  nonterminal remediation retention;
- zero, one, 64, and 65 rows with clean tails;
- poisoned scratch/result reuse, Program rebinding, and unchanged output on
  rejected input;
- warm execution at zero allocations;
- race, 386, field alignment, escape-analysis, repository, vet, build, format,
  module, and benchmark gates with explicit timeouts.
