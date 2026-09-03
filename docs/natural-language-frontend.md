# Reviewed Natural-Language Policies

NornRune's natural-language frontend turns a document into an untrusted policy
proposal for human review. Provider output is never executable, cannot publish
or activate a policy, and cannot create its own valid approval token. The first
release includes a deterministic offline fixture provider and no networked LLM provider.

This workflow does not claim legal correctness, autonomous compliance, or that
the extracted proposal is complete. It preserves uncertainty so a reviewer can
correct or reject the proposal before compilation.

## Trust Boundary

```text
UTF-8 document + page offsets
          |
          v
deterministic segments -> Provider.Extract
          |
          v
untrusted Proposal -> exact citation and structure validation
          |
          v
deterministic review JSON -> reviewer-owned native policy draft
          |
          v
signed approval token -> native decoder -> AST validator -> immutable Program
```

`frontend/natural.Document` owns source bytes, ordered page starts, and a
SHA-256 digest. `Proposal` stores typed item columns and CSR citation edges for
requirements, applicability, assertions, evidence obligations, exceptions,
restrictions, assumptions, and ambiguity. It is deliberately separate from the
native AST and compiled Program.

The provider receives only the document, deterministic segments, explicit
limits, and a proposal builder. It receives no compiler, persistence, registry,
or publication capability. The current `FixtureProvider` supplies repeatable
offline tests and demonstrations. PDF/OCR ingestion and network provider SDKs
are deferred behind the document/provider interfaces.

## Validation

Validation recomputes the document digest and checks every citation quote
byte-for-byte against its half-open UTF-8 source span and page. It also checks
SoA column lengths, CSR edge ownership, one-based IDs, parent ordering, limits,
duplicates, exact citation provenance, evidence and restriction ancestry, and
requirement reachability.

The claim-byte ceiling applies to provider identity, provider version, and item
text in aggregate, so untrusted metadata is rejected before canonical hashing
or review rendering.

The following findings block review approval:

- fabricated, malformed, cross-page, or unverifiable citations;
- ambiguity and conflicts;
- duplicate claims;
- omitted restrictions;
- orphan evidence, exceptions, or assertions;
- malformed or oversized provider output.

Prompt-injection-shaped document text is inert input. It cannot alter schemas,
limits, review requirements, token verification, or publication behavior.

## Review And Approval

The deterministic JSON review view identifies the exact document and canonical
proposal digests and lists each proposal item beside its claim, parent, page,
byte span, and exact quote. Bounded duplicate, conflict, ambiguity, and omitted-
restriction diagnostics remain visible in the artifact while blocking approval.
The reviewer creates or corrects a native policy draft and supplies typed CSR
mappings from every resulting requirement, applicability, clause, assertion,
evidence obligation, resolution, remediation, explanation, outcome, and
assumption row to the proposal items reviewed for it. Every non-ambiguity
proposal item must be mapped. Requirement proposal items must map to native
requirement rows, and every mapping inherits the proposal item's validated
citations.

Approval signs a versioned digest containing:

- the canonical proposal and draft digests;
- reviewer identity;
- issue and expiry times;
- token schema version.

The approval token expires at its explicit expiry timestamp. Any change to
document, proposal, draft, reviewer, time bounds, schema, or signature makes the
token invalid. Signer and verifier implementations are caller-supplied; fixed
Ed25519 keys are used only in tests. Key storage and reviewer authentication are
adapter responsibilities.

Token serialization is a strict bounded binary format with magic `NRAT`.
An identity that cannot fit even with the minimum nonempty signature is rejected
before proposal hashing or calling the signer. The actual signature length is
checked before returned reviewer and signature bytes are owned.
Decoding owns reviewer and signature bytes, rejects truncation and trailing
data, and does not verify authorization by itself. Compilation separately
revalidates the proposal and draft and authenticates the token.

## Compilation Boundary

An approved draft is still decoded through the existing hand-written native
JSON decoder and normal AST validator. Its complete typed semantic-row set must
exactly match the reviewed provenance mappings before atomic lowering. Failed
token checks, JSON decoding, AST diagnostics, or provenance matching leave the
destination Program unchanged.

The natural-language packages do not import service publication or persistence
code. Registry activation remains a separate application decision after
successful review and compilation.

## Limits And Ownership

Default limits bound source bytes, pages, segments, proposal items, citations,
CSR edges, claim and quote slabs, diagnostics, review output, draft bytes,
provenance mappings, and token bytes. Builders and reviewers reuse typed scratch
storage. Published documents, proposals, drafts, tokens, and Programs own their
data.

Extraction and review are cold paths and may allocate within those limits.
Validation and review rendering reuse caller-owned destinations and typed
scratch; warmed validation and review benchmarks report `0 B/op` and
`0 allocs/op`. Evaluator kernels are unchanged.

## Threat Model

Tests cover prompt injection, fabricated citations, omitted restrictions,
ambiguity, conflicts, invalid UTF-8, truncation, malformed CSR columns,
cancellation, token mutation, replay against changed drafts, expiration,
invalid signatures, and bounded fuzz input. Logs and errors may contain fixed
codes and digests, but never source text, citation quotes, credentials,
signatures, or approval tokens.

Provider retention, network retries, model-version drift, OCR fidelity, and
credential handling become provider-adapter obligations if network or PDF/OCR
implementations are added later. They are outside this offline release.
