# Reviewed Natural-Language Policy Frontend Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add an offline, provider-neutral natural-language policy proposal pipeline that verifies exact citations, requires authenticated human approval, and compiles only reviewer-owned native policy drafts.

**Architecture:** A deterministic provider appends untrusted extraction rows into a bounded struct-of-arrays `Proposal`. Reusable validators verify document hashes, exact quote spans, CSR ownership, omissions, ambiguity, and conflicts before producing a deterministic review view. A reviewer-owned native policy draft and its provenance map are signed together with the proposal digest; only a valid, unexpired token permits the existing native JSON decoder, AST validator, and lowerer to produce an immutable Program.

**Tech Stack:** Go 1.27, `crypto/sha256`, `crypto/ed25519` in tests, existing typed slabs and CSR conventions, native JSON policy decoder, AST validator/lowerer, Go fuzzing, and the repository release matrix.

---

## Fixed Constraints

- Follow the approved design in `docs/plans/2026-08-30-reviewed-natural-language-frontend-design.md`.
- Use TDD and observe each intended RED failure before production edits.
- Bound every test, build, vet, fuzz, analyzer, and benchmark command with an outer timeout. Every `go test` also gets `-timeout`.
- Keep provider output non-executable and keep registry/persistence imports out of `frontend/natural` and `internal/frontend/natural`.
- Do not add a network provider, CLI command, PDF/OCR implementation, registry UI, or autonomous publication path.
- Do not add maps, reflection, or per-row allocation to proposal validation, provenance scans, AST validation, compilation, or evaluator kernels.
- Append into caller-owned destinations or reusable typed slabs wherever sizes are known.
- Do not modify evaluator kernels. Warm evaluator benchmarks remain `0 B/op`, `0 allocs/op`.
- Do not commit unless the user explicitly requests it.

### Task 1: Define Bounded Documents, Proposals, And Builders

**Files:**
- Create: `frontend/natural/frontend.go`
- Create: `frontend/natural/proposal.go`
- Create: `frontend/natural/frontend_test.go`

**Step 1: Write enum, document, ownership, and limit tests**

Require stable append-only `ItemKind` values for requirement, applicability,
assertion, evidence, exception, restriction, assumption, and ambiguity. Require
stable diagnostic codes for invalid document, invalid proposal, limit,
citation, duplicate, conflict, ambiguity, omitted restriction, invalid draft,
invalid token, and expired token.

Test that `NewDocument`:

- rejects invalid UTF-8, empty source, nonzero first page, unsorted/duplicate
  page starts, and page offsets beyond source;
- clones source and page starts;
- computes SHA-256 over the exact source bytes;
- applies explicit source/page limits before allocation.

Test a `Builder` API shaped like:

```go
document, err := natural.NewDocument(source, []uint32{0}, limits)
builder.Reset(document.Digest, natural.ProviderInfo{ID: "fixture", Version: "1"})

citation, err := builder.AddCitation(0, natural.Span{Start: 0, End: 12}, source[:12])
requirement, err := builder.AddItem(natural.ItemKindRequirement, 0, []byte("R1"), []natural.CitationID{citation})
_, err = builder.AddItem(natural.ItemKindRestriction, requirement, []byte("must remain local"), []natural.CitationID{citation})
proposal := builder.Finish()
```

Require failed appends to leave every column and edge unchanged. Require
`Finish` to return an owned immutable snapshot that survives builder reuse.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend/natural
```

Expected: FAIL because `frontend/natural` does not exist.

**Step 3: Implement the minimal public model**

Define:

```go
type Limits struct {
	MaxSourceBytes   uint32
	MaxPages         uint32
	MaxSegments      uint32
	MaxItems         uint32
	MaxCitations     uint32
	MaxCitationEdges uint32
	MaxClaimBytes    uint32
	MaxQuoteBytes    uint32
	MaxDiagnostics   uint32
	MaxDraftBytes    uint32
	MaxMappings      uint32
	MaxTokenBytes    uint32
}

type Document struct {
	Source     []byte
	PageStarts []uint32
	Digest     [sha256.Size]byte
}

type Proposal struct {
	DocumentDigest [sha256.Size]byte
	Provider       ProviderInfo

	ItemKinds          []ItemKind
	ItemParents        []ItemID
	ItemTextStarts     []uint32
	ItemTextLengths    []uint32
	ItemCitationStarts []uint32
	ItemCitationCounts []uint16
	ItemBytes          []byte
	ItemCitationIDs    []CitationID

	CitationPages        []uint32
	CitationSourceStarts []uint32
	CitationSourceEnds   []uint32
	CitationQuoteStarts  []uint32
	CitationQuoteLengths []uint32
	CitationQuoteBytes   []byte
}
```

Order fields by lifetime, pointer scan, and access locality after running the
pinned field-alignment analyzer. Keep IDs one-based and CSR ranges zero-based.
Preflight every append using widened arithmetic before mutating storage.

**Step 4: Run GREEN and layout checks**

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend/natural
timeout 300s ./scripts/check-fieldalignment.sh
timeout 30s git diff --check
```

Expected: PASS with no field-alignment finding introduced by the new package.

### Task 2: Add Deterministic Segmentation And The Offline Provider Contract

**Files:**
- Create: `frontend/natural/provider.go`
- Modify: `frontend/natural/frontend_test.go`
- Create: `testdata/frontends/natural/source.txt`
- Create: `testdata/frontends/natural/proposal.json`

**Step 1: Write segmentation and provider contract tests**

Define a cancellation-aware provider contract that cannot receive registry,
persistence, compiler, or publication capabilities:

```go
type Provider interface {
	Info() ProviderInfo
	Extract(context.Context, *Document, []Segment, *Builder, Limits) error
}
```

Require deterministic segmentation by UTF-8 paragraph boundaries with a hard
byte limit. Long paragraphs split only at valid UTF-8 boundaries. Every segment
carries exact source and page ranges. Empty runs do not create segments.

Add `FixtureProvider`, configured from typed fixture rows rather than JSON at
runtime. Require identical proposal bytes/columns across repeated extraction,
early cancellation, no mutation on cancellation, and bounded diagnostics when a
fixture row exceeds limits.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s -run 'TestSegment|TestFixtureProvider|TestProviderCancellation' ./frontend/natural
```

Expected: FAIL because segmentation and provider interfaces do not exist.

**Step 3: Implement segmentation and fixture extraction**

Use one byte scan to build segments into caller-supplied storage:

```go
func AppendSegments(dst []Segment, document *Document, maxBytes uint32, limits Limits) ([]Segment, error)
```

Check `ctx.Err()` before each fixture item, not per source byte. `FixtureProvider`
must append through `Builder` only and must not retain document, segment, or
builder storage.

**Step 4: Run GREEN**

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend/natural
timeout 45s go test -count=1 -timeout 35s -run '^$' -fuzz '^FuzzSegments$' -fuzztime=5s ./frontend/natural
```

Expected: PASS.

### Task 3: Validate Exact Citations, Structure, Conflicts, And Ambiguity

**Files:**
- Create: `frontend/natural/validate.go`
- Modify: `frontend/natural/frontend_test.go`
- Create: `frontend/natural/fuzz_test.go`

**Step 1: Write failing proposal-validation tests**

Require a reusable `Validator`:

```go
diagnostics := validator.Validate(nil, document, proposal, limits)
```

Cover:

- unequal SoA column lengths and invalid enums/IDs;
- non-owned or out-of-range CSR citation edges;
- digest mismatch, invalid pages/spans, and quote bytes not equal to the exact
  source range;
- duplicate sibling claims with the same kind and normalized bytes;
- contradictory restriction/exception or assertion pairs;
- requirements without citations;
- restrictions and evidence obligations without a requirement ancestor;
- cycles, unreachable rows, omitted restriction markers, and any ambiguity row;
- deterministic source-order diagnostics capped at `MaxDiagnostics`.

Require no map allocation in the per-item loop. Use reusable open-addressed
typed hash slots for duplicate detection and reusable color/stack slabs for
reachability.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s -run 'TestValidator|TestCitation|TestConflict|TestAmbiguity' ./frontend/natural
```

Expected: FAIL because validation is missing.

**Step 3: Implement atomic bounded validation**

Add pointerless diagnostics:

```go
type Diagnostic struct {
	Span       Span
	Item       ItemID
	Citation   CitationID
	Code       DiagnosticCode
}
```

Validate outer lengths and total byte/edge limits first. Scan citations once,
then items once, and perform reachability once. Sort only the bounded diagnostic
slice by span, code, item, and citation. Do not copy source excerpts into
diagnostics.

Treat any ambiguity, conflict, unverifiable citation, or omitted restriction as
blocking. Prompt-injection-shaped text has no special execution path; it is
validated as inert source data.

**Step 4: Run GREEN, fuzz, and allocation benchmarks**

Add `BenchmarkValidatorWarm` with a pre-sized diagnostic destination and warmed
validator. Report allocations; any per-item allocation is a failure.

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend/natural
timeout 45s go test -count=1 -timeout 35s -run '^$' -fuzz '^FuzzProposalValidation$' -fuzztime=5s ./frontend/natural
timeout 120s go test -timeout 90s -run '^$' -bench '^BenchmarkValidatorWarm$' -benchmem -count=6 ./frontend/natural
```

Expected: PASS; warmed validation has no allocation proportional to item count.

### Task 4: Produce A Deterministic Redacted Review View

**Files:**
- Create: `internal/frontend/natural/citations.go`
- Create: `internal/frontend/natural/review.go`
- Create: `internal/frontend/natural/review_test.go`

**Step 1: Write failing canonical-digest and rendering tests**

Require canonical proposal hashing independent of backing capacities and a
caller-buffer renderer:

```go
digest, err := reviewer.ProposalDigest(document, proposal, limits)
dst, diagnostics, err := reviewer.AppendReview(dst[:0], document, proposal, limits)
```

The review view must include item kind, parent, exact span/page, quoted source,
claim text, assumptions, conflicts, and ambiguity in source order. It must never
include provider credentials, approval signatures, or hidden process state.

Require byte-identical output across runs, correct JSON escaping without
`encoding/json` reflection, atomic failure, and a hard output bound. Logs and
error strings expose fixed codes and digests only, never source or quote text.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/frontend/natural
```

Expected: FAIL because the internal package does not exist.

**Step 3: Implement canonical append encoding**

Hash a versioned binary framing of scalar metadata, typed columns, CSR edges,
and byte slabs. Include lengths before variable data and use fixed little-endian
integer encoding. Never hash pointer addresses, capacities, timestamps, or map
iteration.

Render review JSON directly into caller-owned `dst` with preflight capacity and
bounded escaping. Reuse `frontend/natural.Validator`; do not create a second
semantic validator.

**Step 4: Run GREEN and warm benchmarks**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/frontend/natural
timeout 120s go test -timeout 90s -run '^$' -bench '^BenchmarkAppendReviewWarm$' -benchmem -count=6 ./internal/frontend/natural
timeout 30s git diff --check
```

Expected: PASS and zero allocation when the caller supplies sufficient output
and reusable reviewer capacity.

### Task 5: Add Reviewer-Owned Draft Provenance And Approval Tokens

**Files:**
- Modify: `frontend/natural/proposal.go`
- Modify: `frontend/natural/frontend.go`
- Modify: `internal/frontend/natural/review.go`
- Modify: `internal/frontend/natural/review_test.go`

**Step 1: Write failing draft and token tests**

Define a reviewer-owned draft containing native policy JSON plus one provenance
row per resulting native semantic row:

```go
type ReviewedDraft struct {
	PolicySource          []byte
	SemanticKinds        []SemanticKind
	SemanticIDs          []uint32
	MappingStarts        []uint32
	MappingCounts        []uint16
	MappingProposalItems []ItemID
}
```

Require every native semantic row to map to reviewed proposal items, every
non-ambiguity proposal item to be covered, and requirement proposal items to map
to native requirement rows. Reject duplicate semantic kind/ID pairs, orphan
mappings, mappings to ambiguity/conflict rows, empty policy source, and
oversized drafts.

Define:

```go
type Signer interface {
	Sign(message []byte) ([]byte, error)
}

type Verifier interface {
	Verify(message, signature []byte) error
}

type ApprovalToken struct {
	SchemaVersion  uint16
	IssuedUnix     int64
	ExpiresUnix    int64
	ProposalDigest [sha256.Size]byte
	DraftDigest    [sha256.Size]byte
	Reviewer       []byte
	Signature      []byte
}
```

Use fixed Ed25519 keys in tests. Cover issue/verify success, wrong proposal or
draft, reviewer mutation, expiry, future issuance outside allowed skew, schema
version, signature mutation, signer/verifier failure, and token-size limits.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s -run 'TestReviewedDraft|TestApprovalToken' ./frontend/natural ./internal/frontend/natural
```

Expected: FAIL because reviewed drafts and tokens do not exist.

**Step 3: Implement digest-bound approval**

Canonicalize the draft using versioned binary framing. Sign one fixed-size
SHA-256 digest of the token payload rather than retaining a temporary dynamic
message. Require caller-supplied `now` and maximum clock skew; never read the
wall clock inside token validation.

Token verification reruns proposal and draft validation before signature
verification. A token is authorization to compile exactly one proposal/draft
pair, not authorization to publish it.

**Step 4: Run GREEN and race tests**

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend/natural ./internal/frontend/natural
timeout 180s go test -count=1 -timeout 150s -race ./frontend/natural ./internal/frontend/natural
```

Expected: PASS.

### Task 6: Compile Only Approved Drafts Through The Native Pipeline

**Files:**
- Create: `internal/frontend/natural/lower.go`
- Create: `internal/frontend/natural/conformance_test.go`
- Modify: `internal/frontend/natural/review.go`

**Step 1: Write failing approval and conformance tests**

Build a deterministic proposal for the three requirement statements in
`internal/fixtures/nornrune-policy.json`, a reviewer-owned draft from
`policies/nornrune/policy.json`, and exact requirement provenance mappings.

Require:

- no compile attempt without a token;
- failure for invalid, expired, mutated, or mismatched tokens;
- native JSON decode and AST diagnostics remain errors, not approvals;
- approved compilation produces the same Program decisions, reasons,
  remediation, explanations, and R1-R5 output as direct native compilation;
- no import of `internal/persistence`, `internal/server`, or registry packages.

Use an API shaped like:

```go
diagnostics, err := lowerer.Compile(
	&dst, document, proposal, draft, token, verifier, now, limits,
)
```

On any diagnostic or error, `dst` remains unchanged.

**Step 2: Run RED**

```bash
timeout 180s go test -count=1 -timeout 150s ./internal/frontend/natural
```

Expected: FAIL because approved lowering is missing.

**Step 3: Implement the existing native path without duplication**

`Lowerer` owns reusable `jsonpolicy.Decoder`, `ast.Builder`,
`compile.Validator`, `compile.Lowerer`, diagnostics, and provenance scratch.
For each call:

1. validate proposal and draft provenance;
2. verify the token;
3. create the canonical NornRune schema through `policies/nornrune.NewSchema`;
4. decode `ReviewedDraft.PolicySource` with explicit `jsonpolicy.Limits`;
5. validate the AST and match every decoded `RequirementID` to draft mappings;
6. lower atomically into the destination Program.

Do not call `internal/server` helpers because that would couple the frontend to
transport and publication code. Keep policy decoder limits in one local
conversion function with explicit tests matching security defaults.

**Step 4: Run GREEN and differential tests**

```bash
timeout 180s go test -count=1 -timeout 150s ./internal/frontend/natural ./internal/conformance ./internal/e2e
timeout 180s env GOARCH=386 go test -count=1 -timeout 150s ./frontend/natural ./internal/frontend/natural
timeout 180s go test -count=1 -tags=purego -timeout 150s ./frontend/natural ./internal/frontend/natural
```

Expected: PASS with R1-R5 unchanged.

### Task 7: Complete Threat, Corpus, Fuzz, And Redaction Coverage

**Files:**
- Modify: `frontend/natural/fuzz_test.go`
- Modify: `internal/frontend/natural/conformance_test.go`
- Create: `testdata/frontends/natural/prompt-injection.txt`
- Create: `testdata/frontends/natural/conflicting.txt`
- Create: `testdata/frontends/natural/ambiguous.txt`
- Create: `testdata/frontends/natural/fabricated-citation.json`

**Step 1: Add failing security and malformed-input tests**

Cover prompt injection as inert source, fabricated citation quotes, omitted
non-negotiable restrictions, conflicting clauses, ambiguity, invalid UTF-8,
truncation, duplicate pages, Unicode byte boundaries, cancellation, oversized
documents, malformed CSR columns, signer failure, token replay against a changed
draft, and redacted errors.

Add deterministic fixture corpus measurements for:

- extracted requirement count;
- exact-citation validity;
- preserved restriction/evidence/exception count;
- unresolved ambiguity count;
- reviewer corrections between proposal and draft.

Do not claim model precision or recall because the first provider is a fixed
offline fixture.

**Step 2: Run RED**

```bash
timeout 180s go test -count=1 -timeout 150s ./frontend/natural ./internal/frontend/natural
```

Expected: at least one new malformed or redaction case fails before its focused
fix.

**Step 3: Implement only focused fixes**

For each failure, add the smallest validation or redaction change. Do not add a
generic dynamic value model, network retry code, provider SDK, or policy
publication adapter.

**Step 4: Run fuzz seeds and race checks**

```bash
timeout 60s go test -count=1 -timeout 50s -run '^$' -fuzz '^FuzzProposalValidation$' -fuzztime=10s ./frontend/natural
timeout 60s go test -count=1 -timeout 50s -run '^$' -fuzz '^FuzzApprovalToken$' -fuzztime=10s ./frontend/natural
timeout 240s go test -count=1 -timeout 210s -race ./frontend/natural ./internal/frontend/natural
```

Expected: PASS.

### Task 8: Document, Audit, And Run Release Gates

**Files:**
- Create: `docs/natural-language-frontend.md`
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/frontends.md`
- Modify: `internal/doccheck/frontends_test.go`
- Modify: `docs/plans/2026-08-20-nornrune-policy-engine.md`

**Step 1: Write failing documentation-contract tests**

Require documentation to state:

- extraction is untrusted and cannot publish policy;
- the first release is deterministic and offline with no LLM provider;
- every semantic row needs reviewed citation provenance;
- ambiguity, conflicts, missing restrictions, and unverifiable citations block
  approval;
- tokens bind exact proposal/draft digests and expire;
- PDF/OCR, network providers, legal-correctness claims, and registry UI are
  deferred;
- source text, quotes, credentials, and tokens are excluded from logs.

Mark Task 53 complete in the ordered roadmap only after all gates pass.

**Step 2: Run RED, write docs, and run focused verification**

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/doccheck
timeout 240s go test -count=1 -timeout 210s ./frontend/natural ./internal/frontend/natural ./internal/doccheck
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 180s go mod tidy -diff
timeout 30s git diff --check
```

Expected: documentation test first fails, then all focused checks pass without
module drift.

**Step 3: Verify evaluator allocation invariants**

Run the existing warmed executor/scheduler benchmarks named in
`docs/performance.md`, plus the new validator/reviewer benchmarks:

```bash
timeout 180s go test -timeout 150s -run '^$' -bench 'BenchmarkExecutor|BenchmarkValidatorWarm|BenchmarkAppendReviewWarm' -benchmem -count=6 ./internal/eval ./frontend/natural ./internal/frontend/natural
```

Expected: existing warmed evaluator cases remain `0 B/op`, `0 allocs/op`; new
results are recorded without an unmeasured performance claim.

**Step 4: Run the complete local release matrix once**

Use a fresh clone and the exact current CI commands before any push. At minimum:

```bash
timeout 300s go test -count=1 -timeout 240s ./...
timeout 360s go test -count=1 -timeout 300s -race -gcflags=all=-d=checkptr=2 ./...
timeout 300s env GOARCH=386 go test -count=1 -timeout 240s ./...
timeout 300s go test -count=1 -tags=purego -timeout 240s ./...
timeout 420s go test -count=1 -tags=integration -timeout 360s ./...
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 300s go run ./cmd/devx policy:check
timeout 300s go run ./cmd/devx results:check
timeout 300s go run ./cmd/devx proto:check
timeout 300s go run ./cmd/devx build
timeout 300s go build -trimpath ./cmd/protoc-gen-nornrune
timeout 300s go run github.com/goreleaser/goreleaser/v2@v2.12.3 check
timeout 30s git diff --check
```

Expected: PASS. Run each command once; diagnose any failure before one bounded
retry.

**Step 5: Review and commit only when requested**

Review all Task 53 production, test, fixture, documentation, and plan files
against the approved design. Fix every confirmed Critical, High, and Medium
finding with a focused RED/GREEN test, then rerun affected packages and the
native suite.

If the user explicitly requests a commit, inspect status/diff/log, stage only
Task 53 files, and commit:

```bash
git commit -m "feat: add reviewed natural language frontend"
```

Do not push until the exact CI matrix has passed from a fresh clone.
