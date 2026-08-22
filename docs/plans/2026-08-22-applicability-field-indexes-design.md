# Applicability And Field Indexes Design

## Scope

Task 12 adds two immutable indexes used by later batch and evaluator tasks:

1. A dense `FieldID -> (ValueKind, kind-local column)` mapping.
2. A conservative applicability index from known symbolic selector facts to
   candidate requirement rows.

The index covers action, resource, and trust selectors without hard-coding
their names. Any symbolic field used by a safely extractable applicability
constraint receives the same treatment. Batch storage, JSON request decoding,
predicate execution, per-batch reused-fact indexes, and policy registries remain
later tasks.

## Alternatives

### Tuple index

An `(action, resource, trust)` tuple can point directly to one candidate mask.
Lookup is simple, but partial selectors require wildcard tuple expansion and
the table grows with the Cartesian product. It also hard-codes three field
roles into an otherwise generic compiler.

### Per-field inverted masks

Each indexed field owns a wildcard mask plus sorted `(SymbolID, mask)` entries.
A query starts with the all-candidates mask and intersects one mask for each
known selector. Missing selectors perform no intersection. This is the chosen
design: storage grows with observed selector values, partial facts are natural,
and adding another symbolic selector does not change the representation.

### Evaluate every applicability expression

Skipping compile-time pruning is simple and semantically safe, but fails the
approved indexing requirement and makes later policy-pack scaling depend on
executing every applicability graph.

## Field Index

`index.Schema` stores parallel `Kinds` and zero-based `Columns` arrays indexed
by `FieldID-1`, plus a fixed count per `ValueKind`. Construction validates all
kinds, assigns each field the next column in its kind, and copies exact-size
storage. `Lookup` validates the one-based ID before returning the kind and
column. The mapping lets Task 13 lay out each field contiguously without maps or
switching on names per row.

Presence storage remains one mask per field. A `ValueKindPresence` field still
receives a kind-local column, while ordinary fields use their `FieldID-1`
presence offset alongside their typed value column.

## Applicability Index

`index.Policy` represents candidate requirement rows with fixed-width bitmaps:

- `AllMask` contains every valid requirement row.
- `FieldIDs` is sorted ascending.
- `FieldValueStarts` and `FieldValueCounts` locate each field's sorted values.
- `WildcardMasks` contains requirements with no extracted constraint for that
  field.
- `Values` contains sorted nonzero `SymbolID`s.
- `ValueMasks` contains wildcard rows plus requirements that allow that value.

Every mask has `ceil(requirementCount/64)` words. Tail bits are always zero.
Construction consumes pointerless constraint columns and CSR value edges,
validates all widened ranges, canonicalizes field and value order, and rejects
duplicate `(field, requirement row)` constraints. The published storage is
exact-size and deterministic regardless of input constraint ordering.

A query takes caller-supplied parallel field, value, and presence columns plus
a caller-supplied destination mask. It validates all shapes before writing,
copies `AllMask`, and intersects masks for present selectors. A present but
unknown symbol intersects the field wildcard mask. A missing selector performs
no intersection, preserving every candidate because applicability may be
unknown rather than false. Lookup and intersection allocate nothing.

## Compiler Extraction

After semantic lowering, `compile.Lowerer` walks each requirement's final
applicability root iteratively:

- A positive symbolic `Equal` contributes one allowed value.
- A positive symbolic `In` contributes its canonical symbol values.
- `All` contributes constraints from each operand because every operand is
  necessary for applicability.
- Any other node terminates extraction for that branch without excluding the
  requirement.
- A second constraint for the same field in one requirement is left wildcard
  rather than risking an incorrect set intersection in this task.

This rule may retain extra candidates but cannot remove a requirement whose
applicability could still be true. Structural CSE and scheduling do not affect
the walk because operands and requirement roots remain final InstructionIDs.
Compiler scratch is pre-sized from instruction, field, and requirement counts
and retained on `Lowerer`; no per-node object or map is introduced.

The compiler builds `index.Schema` from the Program's copied field kinds and
`index.Policy` from extracted constraints, then stores both in the private
output Program before `Freeze`. `Freeze` exact-copies the index columns. Public
lowering remains atomic: no destination Program changes until every index and
the result Resolver have validated.

## Errors And Boundaries

The index package returns bounded sentinel errors for malformed schema,
constraints, query shapes, and fixed-width overflow. Compiler construction
maps malformed generated data to `compile.ErrInvalidGeneratedProgram` and
width overflow to `compile.ErrProgramTooLarge`.

All arithmetic involving CSR starts, counts, mask offsets, and allocation sizes
is widened before conversion. Zero IDs are invalid in stored constraints. A
zero query value is allowed only with a present selector and means the decoded
symbol was not found in the Program symbol table; it therefore selects only
wildcard requirements.

## Verification

Tests cover:

- Stable kind-local field columns and malformed field kinds.
- Action, resource, and trust constraints with exact candidate masks.
- Missing selectors retaining candidates.
- Present unknown symbols retaining only wildcard requirements.
- Multiple known selectors intersecting deterministically.
- Input-order-independent immutable bitmap construction and clear tail bits.
- Conservative compiler extraction through `All` and wildcard handling for
  `Any`, `Not`, negative predicates, and duplicate same-field constraints.
- Frozen index ownership, exact capacities, warm `Lowerer` reuse, race safety,
  and 386 portability.

Benchmarks are unnecessary in Task 12: index lookup is a small number of binary
searches plus whole-mask intersections. Later policy-pack and SIMD tasks will
measure mask intersection and choose dispatch thresholds.
