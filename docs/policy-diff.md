# Semantic Policy Diff

NornRune compares two native policy documents over an explicit finite domain.
The result is `equivalent`, `widened`, `narrowed`, `changed`, or
`inconclusive`. This is bounded regression analysis, not a claim about inputs
outside the declared domain.

## Proof Boundary

Each referenced field needs a typed dimension containing a Missing option.
String dimensions must be marked closed. Evidence-dependent policies need at
least one explicit evidence-set scenario, including an empty set when absence
is relevant. Candidate count is the checked product of selected field options
and evidence scenarios.

Exact equality of canonical executable and result slabs takes an identity path
without generating rows. Otherwise, `equivalent` requires complete exhaustion
of the finite, closed domain. An open dimension, missing dependency, arithmetic
overflow, candidate-budget exhaustion, cancellation, unsupported outcome
catalog, malformed input, or evaluator failure cannot establish equivalence.
Expected bounded uncertainty is reported as `inconclusive`; operational input
and execution failures are returned as errors.

Classification comes from a caller-supplied 4x4 matrix over `Approve`,
`Reject`, `Revise`, and `Escalate`. Every one of the 16 entries declares the
class and whether CI allows it. NornRune does not impose a universal decision
ordering.

## Counterexamples

Search order is deterministic. Dependencies reached from changed instructions
come first, followed by remaining referenced fields in canonical name order.
The first differing candidate becomes an owned counterexample containing:

- typed field values and the selected evidence scenario;
- old and new decisions;
- applied requirement, driver clause and node IDs;
- reason, evidence, remediation, and explanation IDs; and
- source spans for each driver.

The analyzer continues through the bounded domain after finding the first
witness so mixed transition classes and forbidden transitions are not hidden.
Returned data does not borrow policy source, domain, Program, evaluator, or
provider storage.

## Proof Providers

An optional proof provider receives owned source and domain snapshots on the
cold path. Provider equivalence is advisory and is followed by concrete
exhaustion. A proposed changed witness undergoes mandatory native replay through
both Programs. A fabricated witness, decision mismatch, provider error, panic,
timeout, or unsupported claim produces `inconclusive`; provider output cannot
bypass native replay.

## CLI And Exceptions

The command accepts native policy and strict domain JSON explicitly:

```bash
nornrune diff --old-policy old.json --new-policy new.json \
  --domain domain.json --format json
```

Only one input may use `-` for stdin. `--format` is `json` or `text`.
Exit code `0` means equivalent or explicitly allowed, exit code `3` means a
forbidden regression, exit code `4` means inconclusive, exit code `1` means an
operational failure, and exit code `2` means invalid command usage.

Optional exception JSON binds an ID, reason, owner, UTC expiry, exact old and
new source SHA-256 digests, counterexample digest, and decision transition.
Expiry uses adapter-supplied time. A stale or mismatched exception does not
allow a result, and no exception can allow `inconclusive`.

## Performance

Compilation, dependency planning, provider calls, witness ownership, JSON, and
the cold search controller may allocate. Candidate generation writes directly
into reusable numeric slabs. After priming, `BenchmarkCandidateBatch` must
remain `0 B/op` and `0 allocs/op`; evaluator kernels retain their existing zero
allocation contract. See [Performance](performance.md) for commands and
measurement methodology.
