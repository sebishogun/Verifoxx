# Reviewed Natural-Language Policy Frontend Design

**Date:** 2026-08-30

**Status:** Approved

**Roadmap:** Task 53, Phase 19

## Goal

Turn plain-text and page-addressable policy documents into bounded, reviewable
policy proposals without allowing a model or other extraction provider to
publish executable policy. Preserve exact source citations, ambiguity,
conflicts, restrictions, and evidence obligations through explicit human review
and normal NornRune validation.

The first release is offline. It provides a deterministic fixture provider and
the provider contract, but no networked LLM adapter.

## Trust Boundary

The pipeline is:

```text
Document -> segments -> Provider.Extract -> Proposal -> Validate
  -> ReviewArtifact -> ReviewedDraft -> signed ApprovalToken
  -> normal AST validation and compilation -> immutable Program
```

Provider output is always untrusted and non-executable. The natural frontend
has no registry or persistence dependency. It cannot publish or activate a
policy, and a provider cannot create a valid approval token.

Three public objects keep that boundary explicit:

- `Document` owns or borrows bounded UTF-8 source bytes, page-start offsets,
  and a SHA-256 digest.
- `Proposal` contains provider-extracted claims and exact citations but is not
  accepted by the compiler.
- `ReviewedDraft` contains reviewer-corrected semantic content and mappings
  from every semantic row to proposal rows and citations.

Approval binds the canonical proposal digest, reviewed-draft digest, reviewer
identity, issue time, expiry, and schema version. A caller-supplied
signer/verifier authenticates the token. Tests use fixed Ed25519 keys from the
standard library; production key storage remains an adapter concern.

## Data Layout

`Proposal` uses parallel typed columns and CSR edge ranges rather than one
allocated object per extracted item. It has rows for requirements,
applicability, assertions, evidence obligations, exceptions, non-negotiable
restrictions, assumptions, and unresolved ambiguity. Each row references one
or more citation IDs. Citation quotes share one byte slab and carry exact
document byte spans and page indexes.

The reviewed draft preserves NornRune's native requirement, clause, evidence,
resolution, remediation, explanation, and outcome semantics. It does not
collapse the policy into the compatibility frontend's single Boolean decision.
Semantic rows retain proposal and citation provenance through CSR mappings.

All input sizes, pages, segments, proposal rows, citation edges, quote bytes,
diagnostics, semantic rows, and artifact bytes have explicit limits. Mutable
scratch belongs to reusable validators and renderers; published proposals,
drafts, and tokens own their data.

## Validation And Review

Document segmentation is deterministic and preserves byte/page provenance. The
provider appends into caller-owned proposal storage and receives no publishing
capability.

Validation performs one bounded pass over proposal columns and citation edges,
then checks:

- source digest, UTF-8, page ordering, spans, and quote equality;
- column lengths, enum values, limits, CSR ownership, and reachability;
- duplicate claims, omitted citations, conflicting clauses, and unsupported
  requirement shapes;
- explicit preservation of restrictions, exceptions, evidence obligations,
  assumptions, and ambiguity.

The deterministic review artifact presents every proposed semantic item beside
its citations, assumptions, conflicts, and unresolved ambiguity. Missing,
unclear, unverifiable, or conflicting extraction blocks approval. It is not
silently translated into approval logic or a runtime outcome. A reviewer may
correct the draft, but every resulting semantic row must map back to reviewed
proposal rows and citations.

Lowering verifies the token, digests, reviewer, issuance and expiry bounds, and
schema version before invoking existing AST validation and compilation. Any
post-review mutation invalidates the token.

## Diagnostics And Security

Semantic failures return bounded stable diagnostics containing a fixed code,
proposal row, citation row, and source span. Cancellation, signer failure, and
storage failure remain infrastructure errors.

Invalid UTF-8, fabricated or overlapping citations, hash mismatch, duplicate
claims, prompt-injection-shaped text, omitted restrictions, ambiguity,
conflicts, and token tampering all fail closed. Document text is data and cannot
change schemas, limits, review requirements, or publication behavior. Logs and
telemetry may contain fixed codes and hashes, but never source text, quotes,
credentials, signatures, or approval tokens.

## Verification

A deterministic fixture provider and a small public-domain or hand-authored
corpus cover requirements, restrictions, evidence rules, exceptions,
conflicts, and ambiguity. Reviewed drafts are compared with equivalent native
policy fixtures and must compile to the same decisions, reasons, remediation,
and explanations.

Tests cover malformed provider output, fabricated citations, omitted
restrictions, prompt injection, truncation, Unicode, limits, cancellation,
redaction, token mutation and expiry, provider nondeterminism, and conflicting
clauses. Fuzz targets cover documents, proposal columns, citations, review
artifacts, and token decoding.

Extraction and review may allocate within declared limits. Validation and
artifact generation pre-size typed storage, reuse caller-owned destinations,
and avoid per-row maps and reflection. Evaluator code remains unchanged and its
warmed paths must remain `0 B/op` and `0 allocs/op`. Release verification covers
native, pure-Go, 386, race/checkptr, fuzz, vet, field alignment, integration,
documentation, and the full release matrix with explicit timeouts.

## Deferred Work

- Networked LLM providers and provider-specific retention/security policy.
- PDF parsing and OCR implementations beyond the page-addressable ingestion
  interface.
- Registry UI and reviewer workflow adapters.
- Claims of legal correctness or autonomous compliance.
