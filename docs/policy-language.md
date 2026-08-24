# Policy Language

The Verifoxx policy format is bounded JSON that preserves applicability,
assertions, evidence obligations, uncertainty, outcome precedence, provenance,
and corrective actions. The checked-in policy is
[`policies/verifoxx/policy.json`](../policies/verifoxx/policy.json).

## Document Shape

A policy object contains:

| Member | Purpose |
|---|---|
| `schema_version` | format version; currently `1` |
| `name`, `version` | immutable policy identity |
| `assumptions` | bounded explanation templates applied to results |
| `evidence_kinds` | allowed evidence categories |
| `evidence_states` | policy-defined states such as `valid` or `conflicting` |
| `outcomes` | names, precedence, and terminal flags |
| `requirements` | applicability, clauses, evidence, resolutions, and remediation |

Unknown keys, duplicate keys, malformed UTF-8, invalid IDs, unknown catalog
references, graph cycles, excessive nesting, and values with the wrong field
type are rejected. Requirement IDs and request IDs are distinct typed
namespaces even when both render as `R1`.

## Requirement Semantics

Each requirement has an `applies` expression and one or more clauses. A clause
has an `assert` expression, zero or more evidence expressions, a complete
resolution table, and zero or more bounded remediations.

For example, this complete expression from R3 limits applicability to a trusted
internal request for above-standard use of protected data:

```json
{
  "op": "all",
  "args": [
    {"op": "equal", "field": "requester.trust", "value": "trusted_internal"},
    {"op": "equal", "field": "environment.usage", "value": "above_standard_limit"},
    {"op": "equal", "field": "action.dataset", "value": "protected_dataset"}
  ]
}
```

The complete R3 requirement wraps that expression in `applies` and supplies two
clauses with every resolution branch and its explanation data. Use the
checked-in policy as the executable requirement example.

Applicability is evaluated separately from satisfaction. A requirement proven
not applicable contributes no outcome. Missing facts needed to establish
applicability remain unresolved; they are not silently treated as a mismatch.

## Expressions

Expression objects accept exactly the keys required by their operation:

| `op` | Required operands | Meaning |
|---|---|---|
| `all` | `args` | conjunction of one or more expressions |
| `any` | `args` | disjunction of one or more expressions |
| `not` | `arg` | logical negation |
| `equal`, `not_equal` | `field`, `value` | typed equality comparison |
| `less`, `less_equal` | `field`, `value` | typed ordered comparison |
| `greater`, `greater_equal` | `field`, `value` | typed ordered comparison |
| `in` | `field`, non-empty `values` | typed set membership |
| `exists` | `field` | field presence |
| `evidence_matches` | `kind`, `state`, `explanation` | evidence obligation with optional `subject`, `scope`, and `timing` qualifiers |

Literals are symbols, signed integers, Booleans, or RFC 3339 timestamps,
according to the fixed field schema. There are no loops, dynamic functions,
regular expressions, arbitrary Go expressions, reflection, or executable user
code.

`evidence_matches` belongs in a clause's `evidence` list. The batch decoder
resolves request evidence references to typed evidence rows before evaluation.
An evidence match checks kind and state plus any declared qualifier without
performing a database lookup.

## Four-State Truth

The evaluator retains positive and negative support independently:

| Positive | Negative | State |
|---:|---:|---|
| 1 | 0 | true |
| 0 | 1 | false |
| 0 | 0 | unknown |
| 1 | 1 | conflict |

Unknown and conflict are therefore not aliases for false. Sideband reason masks
preserve missing, stale, unclear, unverifiable, wrong-scope, wrong-subject,
wrong-timing, invalid, and conflicting evidence. A clause resolution maps those
conditions to policy-defined outcomes.

## Outcomes And Resolution

The engine uses integer outcome IDs. The policy catalog supplies names and
precedence. The Verifoxx pack defines exactly:

| Outcome | Use |
|---|---|
| `Approve` | every applicable condition is satisfied |
| `Reject` | a known non-negotiable condition is violated |
| `Revise` | a bounded field change or allowed evidence item can correct the request |
| `Escalate` | required evidence is missing, stale, unclear, unverifiable, or conflicting |

Every clause resolves `satisfied`, `false`, `missing`, `stale`, `unclear`,
`unverifiable`, and `conflict`. Outcome reduction uses catalog precedence, not
source order. In the supplied pack `Reject` outranks `Escalate`, which outranks
`Revise`, which outranks `Approve`.

Explanations are precompiled templates. Placeholders are restricted to known
request, policy, outcome, requirement, clause, evidence, reason, field, and
value identifiers. The result adapter renders into caller-owned storage.

## Remediation

Only two corrective actions are supported:

```json
{"kind":"set_field","field":"environment.usage","value":"standard"}
{"kind":"add_evidence","evidence_kind":"usage_limit_adjustment"}
```

This keeps `Revise` bounded. A policy cannot use remediation to relax protected
data disclosure, pre-execution approval, or approved-local-environment rules.

## Validate And Compile

Validate and summarize the embedded production policy through the developer
workflows:

```bash
./cli/devx policy:check
./cli/devx policy:compile
```

For another file, use the product commands directly:

```bash
timeout 120s go run ./cmd/verifoxx validate --policy /path/to/policy.json
timeout 120s go run ./cmd/verifoxx compile --policy /path/to/policy.json
```

`validate` reports stable diagnostics without publication. The database-backed
HTTP and gRPC compile methods persist and activate a valid policy. The CLI
compile path only compiles and summarizes locally.

## Limits

Service policy source is capped at 4 MiB, AST depth at 128, and AST nodes at
65,536. The decoder also bounds catalog rows, requirements, clauses, array
members, string bytes, symbol bytes, and explanation templates. These are
admission controls for deliberately quadratic uniqueness checks and retained
storage; zero or omitted limits are not used by the service runtime.

See [architecture](architecture.md) for the AST and Program layout and
[API](api.md) for remote validation and publication.
