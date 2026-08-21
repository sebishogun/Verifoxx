# AST Normalization And Lowering Design

## Goal

Compile a validated source `ast.Document` into one immutable, self-contained
`program.Program` made of numeric SoA columns and CSR edges. The program owns
all bytes and tables needed after the source AST, schema builder, and compiler
scratch are discarded.

Task 10 owns normalization, instruction scheduling, value/symbol interning,
source maps, semantic roots, and outcome-resolution table construction. Slot
allocation, applicability indexes, batch columns, and execution remain Tasks
11-16.

## Selected Approach

Use a deterministic normalized DAG:

- One variadic opcode for `All` and one for `Any`; nested groups of the same
  kind are flattened into the parent's operand range.
- All expression nodes are pure, so structurally equal candidates may share
  one canonical instruction. Equality includes opcode, typed scalar operands,
  literal-list values, and ordered instruction operands.
- Operand order is preserved. Boolean operands are not sorted, because source
  order is part of deterministic explanation and driver selection.
- Instructions are emitted after their operands. Every operand InstructionID
  is therefore lower than its consumer's ID.
- A deterministic ready scheduler batches currently-ready instructions by
  opcode while preserving the operand-before-consumer invariant. The Program
  records contiguous opcode runs for Task 19; SIMD grouping is therefore a
  Task 10 output rather than an unassigned later rewrite.
- An open-addressed typed hash table finds CSE candidates. Hash collisions are
  resolved by exact column/range comparison. No Go maps or per-node objects are
  introduced.
- A final reverse reachability pass removes normalized group results that are
  bypassed by flattening and are not semantic roots or operands. Compaction
  preserves topological order.

This is preferable to direct source-node lowering, which retains duplicate and
flattened-away work in the evaluator. Binary normalization inflates instruction
counts and complicates source maps; postfix encoding conflicts with liveness,
debugging, and SIMD-stage grouping.

## Instruction Representation

`internal/program/instruction.go` defines a one-byte `Opcode` with zero invalid
and explicit opcodes for the eight compare operations, Evidence, All, Any, and
Not. A one-byte `RootFlags` marks applicability, assertion, and clause-evidence
roots; flags are ORed when CSE merges source nodes.

Instruction columns are parallel and have one row per InstructionID:

```go
Opcodes                []Opcode
Fields                 []schema.FieldID
Values                 []schema.ValueID
ListStarts             []uint32
ListCounts             []uint16
OperandStarts          []uint32
OperandCounts          []uint16
EvidenceKinds          []schema.EvidenceKindID
EvidenceStates         []schema.EvidenceStateID
RootFlags              []RootFlags
InstructionNodes       []schema.NodeID
InstructionSourceStarts []uint32
InstructionSourceEnds   []uint32

ListValues []schema.ValueID
Operands   []schema.InstructionID
```

Unused scalar operands are zero. `In` uses `ListValues`; scalar compares use
`Values`; Boolean and Not instructions use `Operands`; Evidence uses its two
typed IDs. InstructionID is one-based and zero never indexes a row.

The schedule stores result references, not scratch slots. Task 11 adds slot
columns without rewriting operands. A pointerless instruction row is therefore
the same schedule in fast and retained-state debug modes.

Compatible topological runs are recorded separately:

```go
OpcodeRunOpcodes []Opcode
OpcodeRunStarts  []uint32
OpcodeRunCounts  []uint32
```

The ready scheduler chooses opcodes in enum order and temporary canonical
instruction IDs in ascending order. It drains newly-ready instructions of the
same opcode before closing a run. Dependencies can require several runs for one
opcode; Task 19 iterates the run table without gathering or reordering operand
columns. Runs are coarse opcode groups; Task 19 reads each row's aligned Field,
Value, and range columns directly and may subdivide a run by operand shape
without changing InstructionIDs or gathering data.

## Values And Symbols

The program owns one immutable symbol space shared by field names, literals,
policy metadata, outcome names, and evidence catalog names. Lowering accepts
the `schema.Interner` that created the field schema so field-name bytes can be
copied into this program-owned space.

Interning order is deterministic:

1. Field names in FieldID order.
2. AST values in ValueID order.

Duplicate symbol bytes receive one SymbolID. Typed values are also interned by
kind and payload, so duplicate AST literals receive one program ValueID. The
program value table uses one-based payload references for every kind:

```go
ValueKinds []schema.ValueKind
ValueRefs  []uint32
```

A symbol ref is a SymbolID; integer, Boolean, and timestamp refs are one-based
indices into their packed payload columns. Program compare and remediation
rows use translated program ValueIDs, never source-AST ValueIDs.

The frozen symbol table retains open-address hash/ID slots so Task 14 can map
batch symbol bytes without allocation. Lookup compares bytes on hash collision.

The Program remains immutable when a request contains a symbol absent from the
policy. Tasks 13-14 give each caller-owned batch builder a reusable extension
interner. Decoding first probes the Program table; a hit uses the Program
SymbolID. A miss is interned once in the batch-local table and receives:

```text
SymbolID = ProgramSymbolCount + localExtensionID
```

Extension IDs cannot equal a policy literal, while equal unknown bytes within
one request/evidence batch share an ID. Request-to-evidence subject/scope
comparisons therefore remain exact, and two different unknown strings never
collapse to zero. The batch builder sizes and reuses its extension slabs;
nothing mutates a published Program and no per-row allocation is required.
`ProgramSymbolCount` is stored explicitly. One extension interner is shared by
request facts and evidence for the entire batch, so plain SymbolID equality is
complete and Tasks 14-15 need no program-versus-extension range branch.

## Program Ownership

`program.Program` owns:

- Instruction columns and CSR operands/literal lists.
- Program values, packed payloads, symbol bytes, and immutable symbol lookup.
- Copied field-name, field-kind, and field-group columns.
- Requirement and clause roots/CSR edges translated to InstructionIDs.
- Clause satisfied/false outcomes and clause-level remediation alternatives.
- Translated evidence-kind and evidence-state name columns.
- Outcome, remediation, and nine-reason resolution tables plus a validated
  `result.Resolver` borrowing those program-owned slices.
- Source bytes; instruction and source-node mappings; requirement, clause,
  evidence-kind, evidence-state, outcome, and remediation source-span columns;
  policy name, version, and content hash.
- Contiguous opcode-run metadata produced by the grouped topological schedule.

All slice fields precede fixed scalar metadata so the GC can stop scanning
before the scalar tail. New production structs are checked with the pinned
`fieldalignment` analyzer before their first commit; suggestions are reviewed
for locality and false-sharing effects rather than blindly fixed.

The Program is mutable only while lowering. Publication treats every exported
slice as read-only. Lowering builds a new local Program and assigns it to the
caller-provided destination only after all checks succeed, leaving the prior
destination unchanged on error.

## Source Maps

Instruction-to-source columns record the canonical first source NodeID and its
span. Source-to-instruction mappings use CSR:

```go
NodeInstructionStarts []uint32
NodeInstructionCounts []uint16
NodeInstructionIDs    []schema.InstructionID
```

Normal and CSE-merged nodes map to their canonical instruction. A same-kind
group removed by flattening maps to the ordered canonical operand IDs that
represent its normalized contents. This preserves stable navigation without
executing a dead group result and permits a later source node to map to more
than one instruction.

A semantic-root group always emits its own variadic instruction, even when the
same source node is flattened into a same-kind parent elsewhere. Its root must
evaluate only its own operands, never the parent's additional operands. Kept
roots use the normal one-canonical-instruction source map; only a non-root group
whose result is unused maps to its flattened operand range.

CSE canonical ownership is deterministic: the first source node encountered
by the fixed traversal owns `InstructionNodes` and the instruction span; every
equivalent source node still has its own source-to-instruction range.

## Traversal And Compaction

Lowering is iterative. A reusable frame stack performs postorder traversal;
recursion is prohibited because validated documents may contain thousands of
nested nodes.

Source NodeIDs are seeded in ascending order. This makes the canonical CSE
owner independent of map iteration or semantic-root ordering. Validated input
guarantees a DAG and that every safe node is reachable from semantic roots.

After candidate emission:

1. Requirement applicability, clause assertion, and clause evidence roots mark
   their canonical instructions.
2. A reverse topological pass propagates reachability through operand IDs.
3. Dead normalized results are removed from the temporary DAG.
4. A deterministic Kahn-style ready scheduler emits live instructions in
   opcode batches while preserving dependency order and records opcode runs.
5. Operand, semantic-root, and source-map IDs are remapped once.
6. Exact-size slices are copied into the immutable Program.

Lowerer scratch is grouped by compilation lifetime and retained between calls.
It uses typed slices for visit state, frames, hashes, canonical IDs, temporary
operands, remap IDs, and root marks. Capacity is established from document
column lengths before per-node work; no per-node heap allocation occurs.

## Semantic Tables

RequirementID remains a policy-space ID and is copied in requirement-row
order. Applicability roots and clause assertion/evidence roots are translated
to InstructionIDs. Requirement-to-clause edges preserve source order.

ClauseID and RuleSetID remain dense one-based row IDs, with the invariant:

```text
RuleSetID == ClauseID
```

Each clause emits nine reason rows in fixed `truth.ReasonID` order:

| Runtime reason | Source outcome |
|---|---|
| Missing | OnMissing |
| Stale | OnStale |
| Unclear | OnUnclear |
| Unverifiable | OnUnverifiable |
| WrongScope | OnUnverifiable |
| WrongSubject | OnUnverifiable |
| WrongTiming | OnUnverifiable |
| Invalid | OnUnverifiable |
| Conflict | OnConflict |

Clause remediation alternatives are appended once and ranges may be shared by
several reason rows. A row receives those alternatives only when its selected
outcome is nonterminal. Terminal Reject/Escalate/Approve rows expose no
corrective action. This derives the required Missing-to-Revise alternatives
and Stale-to-Escalate no-remediation behavior from policy data rather than
decision labels.

Satisfied and false outcomes remain separate clause columns. Task 16 feeds
them into the same outcome precedence reducer and uses the clause remediation
range only for a nonterminal winner.

AST remediation kinds are converted explicitly to runtime result kinds. All
OutcomeID, RemediationID, EvidenceKindID, EvidenceStateID, ClauseID, and
RequirementID numbering is preserved.

## API And Errors

The cold convenience API and reusable API are:

```go
func Lower(doc *ast.Document, fields *schema.Schema, symbols *schema.Interner) (*program.Program, error)

type Lowerer struct { /* reusable typed scratch */ }
func (l *Lowerer) Lower(dst *program.Program, doc *ast.Document, fields *schema.Schema, symbols *schema.Interner) error
```

The method validates input with its reusable Validator before reading unchecked
columns. Invalid/nil input returns `ErrInvalidDocument`; missing or mismatched
field-symbol bytes return `ErrInvalidSymbols`; a policy with no requirements or
clauses returns `ErrEmptyPolicy`; fixed-width count/address overflow returns
`ErrProgramTooLarge`; generated result-table rejection returns
`ErrInvalidGeneratedProgram`.

Detailed validation diagnostics remain the caller's responsibility through
`Validator`; lowering returns a bounded stage error. No malformed input panics,
and destination state changes only on success.

`symbols` must be the same interner that assigned the schema's FieldName
SymbolIDs. Go cannot infer semantic interner identity from a numeric ID; the
lowerer detects missing IDs but cannot detect a different interner containing a
different valid byte value at the same ID. The contract is explicit at the API.

A `Lowerer` owns mutable reusable scratch and is not safe for concurrent use.
Separate compiler workers use separate Lowerers; the emitted Program is
immutable and safe for concurrent readers after publication.

## Tests

Task 10 tests cover:

- Same-kind nested group flattening and dead-group removal.
- Ordered variadic operands and exact post-compaction instruction counts.
- Root groups flattened into another expression still retaining their own
  result and source mapping.
- Iterative deep traversal without recursion.
- Topological operands lower than their consumers.
- Deterministic contiguous opcode runs without dependency violations.
- Structural CSE for duplicate leaves and duplicate flattened groups, including
  stable first-node ownership and many-to-one source maps.
- Deterministic byte-for-byte program columns across repeated lowering.
- Scalar and `In` literal translation into canonical program values.
- Integer-only instruction operands and absence of node pointers.
- Requirement/clause roots, `RuleSetID == ClauseID`, all nine reason mappings,
  runtime remediation-kind conversion, and terminal remediation suppression.
- Program-owned symbol lookup for field names, literals, metadata, outcomes,
  and evidence catalogs.
- Frozen-symbol misses reserved for batch-local extension IDs without Program
  mutation or zero-ID aliasing.
- Retained requirement, clause, and catalog source spans.
- Invalid, empty, oversized, and unchanged-destination error paths.
- Production `fieldalignment`, race, 386, full repository, vet, build, format,
  module, and diff checks with explicit timeouts.

Task 10 adds no benchmark because compilation is a policy-load path and the
master plan assigns no lowering benchmark. Task 11 benchmarks the first
runtime-memory consequence: scratch bytes with and without liveness reuse.
