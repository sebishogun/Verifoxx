# Compatibility Frontends

NornRune accepts its complete native semantic JSON policy and four bounded
compatibility frontends: CEL, Rego, Cedar, and generated Protobuf bindings.
These adapters are not drop-in replacements for the complete upstream
languages. They reject syntax outside the matrices below rather than silently
approximating it.

CEL, Rego, and Cedar parsing is an explicit CLI cold-path operation. Protobuf is
a compile-time generator; runtime Protobuf policy selection is rejected. Every
accepted source becomes the same typed semantic policy and immutable Program,
then uses the shared evaluator kernels. Parser libraries, descriptor reflection,
and source-language switches do not enter per-row evaluation.

## CLI Selection

`compile`, `validate`, and `evaluate` default to `--format native`. Native
behavior and output are unchanged when the flag is omitted. A compatibility
format requires explicit policy and binding files; formats are never inferred
from file names or source text.

```bash
timeout 120s go run ./cmd/nornrune compile \
  --format cel --policy policy.cel --bindings bindings.json
timeout 120s go run ./cmd/nornrune validate \
  --format rego --policy policy.rego --bindings bindings.json
timeout 120s go run ./cmd/nornrune evaluate \
  --format cedar --policy policy.cedar --bindings bindings.json \
  --requests requests.json --evidence evidence.json
```

Only one input may use `-` for stdin. `--bindings` is invalid with native JSON.
Runtime Protobuf is invalid; generate and compile its static binding instead.
The HTTP/gRPC publication API and persisted registry policies remain native JSON
in this task.

### Binding JSON

Bindings form a strict, versioned declaration environment. Unknown fields,
duplicates, trailing JSON, duplicate source names or targets, invalid enums,
and undeclared source references are rejected.

```json
{
  "name": "access-policy",
  "version": "v1",
  "decision": "allow",
  "fields": [
    {
      "source": "team",
      "target": "subject.team",
      "kind": "string",
      "group": "subject"
    },
    {
      "source": "count",
      "target": "context.count",
      "kind": "integer",
      "group": "context"
    },
    {
      "source": "enabled",
      "target": "context.enabled",
      "kind": "boolean",
      "group": "context"
    }
  ]
}
```

Kinds are `string`, `integer`, or `boolean`. Groups and target prefixes are
`subject`, `action`, `resource`, `output`, or `context`. Rego uses `decision` to
name its complete Boolean rule; CEL and Cedar ignore it.

## Decision Mapping

| Semantic result | NornRune decision |
|---|---|
| True | `Approve` |
| False | `Reject` |
| Missing, stale, unclear, conflicting, or unverifiable | the selected Default |
| Default `escalate` | `Escalate` |
| Default `reject` | `Reject` |

CEL and Cedar select Default `escalate`. Rego's `default <decision> := false`
selects Default `reject`; omission selects `escalate`, while a true default
contributes a statically approving rule. Unlike an OPA undefined query, a
defined Rego rule body that evaluates False without a default maps to `Reject`;
missing required input still maps to `Escalate`. Rego presence-aware negation
preserves the supported OPA absence behavior. Cedar combines policies as
`any(permit) && !any(forbid)`, so forbid precedence rejects an otherwise
matching permit. With no permit policy, Cedar is statically False.

## CEL Matrix

Pinned checker/parser: `cel.dev/cel-go v0.32.0`.

| Support | Capabilities | Boundary |
|---|---|---|
| Supported | `boolean_literals`, `scalar_variables`, `logical_operators` | Boolean constants, declared scalar names, and `&&`, `||`, `!` |
| Restricted | `object_selection`, `scalar_comparisons`, `constant_list_membership` | Declared static selections; typed scalar comparisons; scalar membership in a non-empty constant list |
| Rejected | `function_calls`, `macros_and_comprehensions`, `maps_messages_and_optionals` | Calls, macros, comprehensions, dynamic maps/messages, and optional values |

## Rego Matrix

Pinned parser: `github.com/open-policy-agent/opa v1.19.1`, Rego v1 syntax.

| Support | Capabilities | Boundary |
|---|---|---|
| Supported | `rego_v1_modules`, `complete_boolean_decisions`, `boolean_defaults`, `multiple_rules`, `conjunctive_bodies` | One package, complete Boolean rules, optional Boolean default, OR across same-name rules, AND within bodies |
| Restricted | `static_input_references`, `scalar_comparisons`, `constant_membership`, `presence_aware_negation` | Declared `input` paths, typed scalar operations, non-empty constant arrays or sets, and bounded negation |
| Rejected | `imports_and_data`, `functions_and_recursion`, `variables_and_comprehensions`, `with_and_unsupported_builtins` | Imports/data access, helper functions, recursion, unification variables, comprehensions, `with`, and unlisted built-ins |

## Cedar Matrix

Pinned parser: `github.com/cedar-policy/cedar-go v1.8.0`.

| Support | Capabilities | Boundary |
|---|---|---|
| Supported | `static_permit_forbid`, `boolean_composition`, `forbid_precedence` | Static permit/forbid policies, Boolean conditions, and deny-overrides composition |
| Restricted | `equality_scopes`, `context_scalar_conditions` | Equality-only principal/action/resource scopes and declared scalar context fields |
| Rejected | `entity_hierarchy_and_attributes`, `sets_records_and_extensions`, `templates_and_annotations` | Entity graph lookup, entity attributes, collection/record/extension operations, templates, and annotations |

## Protobuf Matrix

Pinned runtime/generator API: `google.golang.org/protobuf v1.36.12`. The
`protoc-gen-nornrune` plugin reads proto3 descriptors and emits a static Go
`frontend.BindingSet`; generated request messages are not decoded by evaluator
kernels.

| Support | Boundary |
|---|---|
| Supported | Top-level proto3 messages with `policy_name`, `policy_version`, and `cel_expression`; singular `string`, `bool`, signed `int32`/`int64` variants; one `canonical_target` per field |
| Restricted | The embedded expression is the bounded CEL subset; target prefixes must be one of the five canonical groups |
| Rejected | proto2, repeated fields, oneof, optional fields, nested messages, maps, enums, bytes, unsigned/fixed integers, floating-point fields, missing options, duplicate bindings, and unknown target groups |

Install or build the local plugin and regenerate deterministically through the
repository workflow:

```bash
timeout 180s go build -o .nornrune/tools/protoc-gen-nornrune ./cmd/protoc-gen-nornrune
timeout 300s env PATH="$PWD/.nornrune/tools:$PATH" go run ./cmd/devx proto:gen
timeout 300s env PATH="$PWD/.nornrune/tools:$PATH" go run ./cmd/devx proto:check
```

The custom options are declared in `frontend/proto/options.proto`; the pinned
Buf recipe is `buf.frontend.gen.yaml`.

## Reviewed Natural-Language Input

Natural-language extraction is not a fifth compatibility format and is not
accepted through `--format`. It produces an untrusted, non-executable proposal
with exact citations. A human reviewer owns the native JSON draft, provenance
mapping, and digest-bound approval token required before normal native
compilation. The offline release includes only a deterministic fixture provider;
network models, PDF/OCR, CLI publication, and legal-correctness claims are
deferred. See the [reviewed natural-language frontend guide](natural-language-frontend.md).

## Shared Limits

Default limits apply independently to every compatibility compilation:

| Resource | Limit |
|---|---:|
| Source or binding input | 4 MiB |
| Semantic nodes | 65,536 |
| Expression depth | 128 |
| Declared fields | 4,096 |
| Scalar literals | 131,072 |
| Child edges | 65,536 |
| String bytes | 1 MiB |
| Diagnostics | 128 |

Sources must be valid UTF-8. Diagnostics are deterministic, bounded, and carry
half-open UTF-8 byte offsets into the original source. Exceeding a limit rejects
the policy; it does not truncate semantics.

## Verification Scope

The differential corpus checks accepted and rejected fixtures against the
pinned official parsers/checkers, exact source spans, stable capability names,
and equivalent CEL/Rego/Cedar decisions for Boolean, integer, string,
conjunction, disjunction, negation, and missing-field cases. Fuzz seed tests and
short bounded local fuzz campaigns cover malformed source and semantic tables.
This is not an exhaustive upstream conformance suite or a full-language
compatibility claim.

Stage-separated benchmarks report official parsing/checking, translation,
shared lowering, cold compilation, and warmed evaluation. Warm evaluator paths
must remain allocation-free. There is no fixed performance claim and no
cross-engine speed claim; reproduce measurements using the commands in the
[performance guide](performance.md).
