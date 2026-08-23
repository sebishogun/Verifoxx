# Verifoxx Product CLI Design

**Status:** Approved

**Date:** 2026-08-23

## Goal

Add a scriptable Cobra CLI over the existing policy decoder, validator,
compiler, batch decoder, evaluator, and result encoder. The default evaluation
must run entirely from embedded data and emit the existing machine-readable
golden output without requiring PostgreSQL or network services.

## Scope

Task 26 adds these commands:

```text
verifoxx evaluate
verifoxx validate
verifoxx compile
verifoxx explain <request-id>
verifoxx simulate <request-id> --set field=value
```

The root command without arguments continues to display help. Service,

## Decision

Use thin Cobra commands over one reusable in-memory execution pipeline. Cobra
owns argument parsing and help. The CLI adapter owns input loading and command
presentation. Existing core packages continue to own parsing, semantics,
compilation, evaluation, and result encoding.

Two alternatives were rejected:

- Keeping the pipeline in `internal/app` would preserve a monolithic dispatcher
  and make later adapters depend on process concerns.
- Starting the scheduler or future service for every command would add worker
  lifecycle machinery to a five-row offline command before Task 32 defines the
  service boundary.

## Package Boundaries

Pin `github.com/spf13/cobra` at `v1.10.2`.

Add a reusable Go package beside `policies/verifoxx/policy.json`. The package
embeds that semantic policy and constructs its typed field schema:

```text
requester.team
requester.trust
action.type
action.output
action.dataset
environment.execution_env
environment.usage
```

This package is the Verifoxx policy pack. It prevents the CLI from embedding
`internal/fixtures/verifoxx-policy.json`, which is the original natural-language
assignment source rather than compiler input. It also removes the production
schema from conformance-test-only code and gives later adapters one source of
truth.

`internal/adapters/cli` contains the Cobra tree, input resolution, shared
pipeline state, row selection, overrides, deterministic metadata output, and
exit-code mapping. `internal/app` becomes a thin process adapter. The existing
`app.Run(args, stdout, stderr)` entry point remains available, while an
stdin-aware entry point is used by `cmd/verifoxx`.

## Input Contract

Commands accept these shared paths where relevant:

```text
--policy PATH
--requests PATH
--evidence PATH
```

An omitted path selects embedded data. A path of `-` reads stdin. At most one
input may use stdin in one invocation. File reads and stdin reads are adapter
operations and occur before decoding.

External policies use the Verifoxx policy-pack schema. A future schema-bearing
pack format is outside Task 26.

## Command Contract

### `evaluate`

Compile the selected policy, decode the selected requests and evidence,
evaluate the full batch, and encode it with `jsonresult.Encoder`. With no flags,
stdout must be byte-identical to `results/requests.json`.

### `validate`

Decode and semantically validate a policy without lowering it. A valid policy
emits:

```json
{"valid":true,"diagnostics":[]}
```

Semantic validation failure emits deterministic JSON diagnostics containing
the stable code, table, one-based row, member, source span, and available typed
IDs, then exits with status 1. Malformed transport input is an operational
decode error and is reported on stderr.

### `compile`

Decode, validate, and lower a policy. Emit deterministic JSON containing its
name, version, SHA-256 content hash, instruction count, requirement count,
clause count, truth-slot count, and reason-slot count. This is a compilation
summary, not a serialized executable artifact.

### `explain <request-id>`

Decode the selected batch, locate the strongly typed request ID, copy that row
and its referenced evidence into a compact one-row `eval.Builder`, evaluate the
one-row batch, and emit the normal result envelope with one result.

### `simulate <request-id> --set field=value`

Use the same compact one-row path as `explain`, applying one or more overrides
while the destination builder is active. Field names resolve through compiled
field metadata. Values are parsed according to the field kind and symbols are
interned through the destination builder. Unknown fields, malformed
assignments, incompatible values, duplicate target fields, and a missing
`--set` are usage errors. The command emits the simulated one-result envelope;
it does not mutate embedded or caller-provided input.

The first version supports symbol, integer, timestamp, and Boolean values.
Presence-only fields accept `true` or `false`, where false leaves the copied
field absent.

## One-Row Compaction

The source batch remains borrowed from its decoder builder. A second builder
creates the selected row:

1. Copy the request ID.
2. Walk fields in stable `FieldID` order and copy present typed values.
3. Replace fields named by simulation overrides instead of copying their source
   values.
4. Resolve source extension symbols through the source builder and intern them
   into the destination builder.
5. Copy only evidence records referenced by the selected row, remapping their
   CSR references to dense zero-based rows.
6. Finish the one-row batch before evaluation.

This avoids JSON reparsing, post-finish mutation, result projection, and changes
to evaluator APIs. Work is bounded by one row's fields and evidence and remains
outside evaluator kernels.

## Errors And Exit Codes

The process contract is:

| Code | Meaning |
|---:|---|
| 0 | Command completed successfully |
| 1 | Input, decode, validation, compile, evaluation, encoding, or write failure |
| 2 | Invalid command, flags, arguments, request ID, or simulation assignment |

Cobra's automatic error and usage printing is disabled. The adapter renders
each error once to stderr. Successful machine output is the only content sent
to stdout. A stdout write failure returns 1 without attempting another stdout
write.

## Performance And Ownership

CLI construction, file loading, and compilation are once-per-process work. Row
selection is once per `explain` or `simulate` invocation. No command adds work,
callbacks, logging, maps, reflection, locks, or allocation to per-node or
per-row evaluator kernels.

The full-batch evaluator and result encoder retain their warm zero-allocation
contracts. Command summaries and diagnostics use append-based JSON writers so
ordering and escaping remain explicit.

## Tests

Command tests inject input loading and capture stdin, stdout, and stderr in
memory. They cover:

- Exact no-flag `evaluate` output
- External policy, request, and evidence inputs
- Stdin selection and conflicting stdin flags
- Valid and invalid policy validation
- Stable compile metadata
- Request lookup and one-result explanation
- Symbol, integer, timestamp, Boolean, and presence overrides
- Invalid fields, types, IDs, assignments, and duplicate overrides
- Cobra usage errors and process exit codes
- Decode, compile, evaluate, encode, and writer failures where injectable
- JSON-only stdout and bounded stderr errors
- Existing help and version behavior

Completion requires focused tests followed by native, purego, 386,
race/checkptr, vet, formatting, whitespace, and full-suite gates. Running
`go run ./cmd/verifoxx evaluate` must reproduce `results/requests.json` exactly.
