# API

Verifoxx exposes the same transport-independent policy service through strict
JSON HTTP and protobuf gRPC adapters. Both apply request deadlines, bounded
admission, body/message limits, and the same immutable policy selection.

## Start The Service

The simplest local service environment is Compose:

```bash
./cli/devx full
```

For a database that is already migrated, set a runtime-role URL and run the
source service directly:

```bash
export VERIFOXX_DATABASE_URL='postgresql://verifoxx_runtime:verifoxx-runtime-local@127.0.0.1:5432/verifoxx?sslmode=disable'
export VERIFOXX_AUDIT_MODE=required
./cli/devx serve
```

Defaults are HTTP `127.0.0.1:8080` and gRPC `127.0.0.1:9090`. See
[database](database.md) for migration setup and [operations](operations.md) for
configuration.

## HTTP Routes

| Method | Path | Request | Success |
|---|---|---|---|
| `POST` | `/v1/policies/validate` | raw policy JSON object | validation JSON; `200` valid or `422` diagnostics |
| `POST` | `/v1/policies/compile` | raw policy JSON object | persisted, active policy metadata |
| `POST` | `/v1/evaluate` | evaluation envelope | canonical result JSON |
| `GET` | `/v1/policies/{sha256}` | 64 hexadecimal hash in path | policy metadata |
| `GET` | `/readyz` | none | dependency and admission readiness |
| `GET` | `/healthz` | none | readiness compatibility alias |
| `GET` | `/livez` | none | process liveness |
| `GET` | `/metrics` | none | Prometheus text exposition |

POST requests require `Content-Type: application/json`; parameters such as
`charset=utf-8` are accepted. Content encoding, URL query strings, unknown
envelope members, duplicate members, trailing JSON values, and non-object
request/evidence documents are rejected.

## HTTP Examples

### Validate A Policy

```bash
curl -fsS \
  -H 'Content-Type: application/json' \
  --data-binary @policies/verifoxx/policy.json \
  http://127.0.0.1:8080/v1/policies/validate
```

A valid response is:

```json
{"diagnostics":[],"valid":true}
```

Diagnostics contain `code`, `table`, `member`, `row`, a half-open source
`span`, and typed one-based IDs. Validation does not publish the policy.

### Compile And Activate A Policy

```bash
curl -fsS \
  -H 'Content-Type: application/json' \
  --data-binary @policies/verifoxx/policy.json \
  http://127.0.0.1:8080/v1/policies/compile
```

The response identifies the immutable compiled shape:

```json
{
  "name":"verifoxx",
  "version":"1.0.0",
  "sha256":"<64 lowercase hexadecimal characters>",
  "instructions":14,
  "requirements":3,
  "clauses":5
}
```

Shape counts are shown for illustration; clients must consume the returned
values rather than pinning them. Compile persists the canonical policy and
derived graph before local activation.

### Evaluate One Request

`requests` and `evidence` are the same complete objects accepted by the CLI.
The optional `policy_sha256` selects a published version; omitting it selects
the active policy.

```bash
curl -fsS \
  -H 'Content-Type: application/json' \
  --data-binary @- \
  http://127.0.0.1:8080/v1/evaluate <<'JSON'
{
  "requests": {
    "schema_version": 1,
    "pack": "verifoxx",
    "requests": [{
      "id": "R1",
      "requester": {"team":"external_partner","trust":"external"},
      "action": {"type":"aggregate_analysis","output":"aggregate_counts","dataset":"protected_dataset"},
      "environment": {"execution_env":"local_approved_env","usage":"standard"},
      "evidence_refs": ["E1","E2"]
    }]
  },
  "evidence": {
    "schema_version": 1,
    "pack": "verifoxx",
    "evidence": [
      {"id":"E1","type":"approval_record","attributes":{"status":"valid","timing":"before_execution","reviewer":"designated_reviewer","timestamp_state":"current"}},
      {"id":"E2","type":"execution_environment_attestation","attributes":{"subject":"local_approved_env","status":"verified","attestation_state":"valid"}}
    ]
  }
}
JSON
```

Success is one canonical result document with policy identity, engine version,
and a `results` array. Every result contains the decision, bounded rationale,
applied requirements, used evidence, missing or conflicting evidence,
assumptions, unresolved uncertainty, and structured remediation. The full
checked-in example is [`results/requests.json`](../results/requests.json).

### Select A Published Policy

Add this member to the complete evaluation envelope from the preceding example:

```json
"policy_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
```

The hash must be exactly 32 bytes represented by 64 hexadecimal characters.
An unknown valid hash returns `policy_not_found`.

## HTTP Errors

Errors use one envelope:

```json
{"error":{"code":"invalid_request","message":"request is invalid"}}
```

Important mappings are:

| Status | Code | Condition |
|---:|---|---|
| `400` | `invalid_json`, `invalid_request` | malformed or semantically invalid input |
| `404` | `not_found`, `policy_not_found` | route or policy absent |
| `405` | `method_not_allowed` | wrong method; `Allow` is set |
| `413` | `body_too_large`, `output_too_large` | configured body/output limit |
| `415` | `unsupported_media_type` | POST is not JSON or is content-encoded |
| `422` | `invalid_policy` or validation body | policy validation failed |
| `408` | `request_canceled` | caller cancellation |
| `504` | `deadline_exceeded` | request timeout |
| `503` | `service_busy`, `service_unavailable`, `audit_unavailable` | bounded capacity, shutdown, dependency, or required audit failure |

Retryable `503` responses set `Retry-After: 1`. Internal details and database
credentials are not returned.

## gRPC

The protobuf contract is
[`api/proto/verifoxx/v1/verifoxx.proto`](../api/proto/verifoxx/v1/verifoxx.proto):

| RPC | Shape |
|---|---|
| `ValidatePolicy` | unary raw policy bytes to diagnostics |
| `CompilePolicy` | unary raw policy bytes to metadata |
| `EvaluateBatch` | unary request/evidence JSON bytes to canonical result bytes |
| `EvaluateStream` | bidirectional stream with one result for each input, in receive order |

An empty `policy_sha256` selects the active policy; otherwise it must contain
raw 32-byte SHA-256 data, not hexadecimal text. Protobuf values are translated
at the adapter and do not enter the evaluator.

Exercise the public gRPC batch API with the bounded repository client:

```bash
timeout 60s go run ./cmd/loadgen \
  -protocol grpc -target 127.0.0.1:9090 \
  -requests 1 -concurrency 1 -timeout 30s
```

gRPC maps invalid input to `InvalidArgument`, limits to `ResourceExhausted`,
unknown policy hashes to `NotFound`, cancellation/deadlines to their matching
codes, and admission, dependency, or audit failures to `Unavailable`.

## Limits And Security

HTTP body and gRPC message limits share `VERIFOXX_MAX_BODY_BYTES`; the default
is 8 MiB and the hard ceiling is 64 MiB. Policy source has a separate 4 MiB hard
ceiling. The default request deadline is 30 seconds and the default batch row
limit is 65,536.

The adapters provide input bounds and safe error contracts, not authentication
or authorization. Default and Compose listeners are host-loopback only. Put an
authenticated TLS proxy in front of any non-local deployment and use TLS for
PostgreSQL.
