# Repository Guide

## Sources Of Truth

- `docs/plans/2026-08-20-nornrune-policy-engine-design.md` is the approved architecture; `docs/plans/2026-08-20-nornrune-policy-engine.md` is the ordered implementation plan.
- `docs/archive/source-material/` preserves the original brief that seeded the baseline conformance policy. It is historical provenance only, not current product requirements.

## Development

- The module is `github.com/sebishogun/nornrune`, targets Go 1.27, and enters through `cmd/nornrune`.
- Run all tests with `go test -timeout 60s ./...`; use `-count=1` when fresh evidence matters.
- Bound every test, benchmark, build, vet, and fuzz command with an explicit timeout. Never use watch or repeat loops.
- Keep per-node, per-row, and per-request paths allocation-free through capacity hints, reusable typed slabs, SoA columns, and CSR edges. Verify with `-benchmem` and `-gcflags=-m`.
- Order struct fields deliberately when each type is introduced: reduce padding and GC pointer-scan bytes while preserving access locality and cache-line isolation. `gofmt` only aligns source text; audit production types with the pinned `fieldalignment` analyzer and review each suggested reorder instead of applying fixes blindly.
- PostgreSQL, JSON, CLI, TUI, HTTP, and gRPC are adapters. They must not introduce maps, reflection, database calls, or string conversion into evaluator kernels.

## Product Semantics

- Model policy requirements as a reusable intermediate semantic representation. Never hardcode outcomes for the baseline request IDs (`R1`-`R5`) or reduce the representation to flat field extraction.
- Requirement IDs `R1`-`R3` and baseline request IDs `R1`-`R5` occupy different namespaces. Name their types and variables accordingly to avoid mixing them.
- Decisions are exactly `Approve`, `Reject`, `Revise`, or `Escalate`:
  - `Reject` is for a violated non-negotiable condition.
  - `Revise` requires a bounded corrective change, such as reduced scope or usage, or an allowed additional evidence item.
  - `Escalate` is required for missing, incomplete, stale, unclear, or conflicting required evidence, and for an unverifiable execution environment.
- Protected-data processing must use a verified approved local environment. Disclosure restrictions and pre-execution approval cannot be relaxed, including for trusted internal teams.
- Usage above the standard limit is available only to trusted internal teams with a specific, current usage-adjustment approval.

## Conformance Artifacts

- The baseline conformance policy, requests, and evidence are embedded read-only through `internal/fixtures` and re-evaluated against `results/requests.json` and `testdata/golden/requests.json` by the `devx` gates.
- The one-page semantic model summary lives in `docs/semantic-model.md`; the development tooling disclosure lives in `docs/ai-usage.md`.
- Machine-readable results identify the request, decision, bounded rationale, applied requirements, used evidence, missing or conflicting evidence, assumptions, and unresolved uncertainty.
- Prioritize edge cases around non-negotiable violations, bounded revisions, missing attestations, and stale or conflicting approvals.
