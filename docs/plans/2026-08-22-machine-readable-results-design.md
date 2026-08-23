# Machine-Readable Result Encoding Design

**Date:** 2026-08-22
**Status:** Approved by the Phase 10 roadmap and delegated implementation authority
**Roadmap:** Task 25

## Goal

Encode a compiled policy and one numeric `result.Batch` as the required
machine-readable JSON without constructing row objects, converting borrowed
bytes to strings, or allocating after caller-owned buffers are primed. Output
must be deterministic and byte-identical to `results/requests.json`.

## Considered Approaches

An `encoding/json` DTO is concise but duplicates the result projection and
allocates strings, slices, and reflection state per batch. A generic JSON token
writer removes reflection but adds an abstraction that this one fixed schema
does not need. The selected approach is a schema-specific append encoder: it
writes fixed punctuation directly, appends numeric IDs with `strconv`, and
escapes borrowed UTF-8 bytes in one pass.

## API And Ownership

A zero-value `jsonresult.Encoder` is reusable but must first bind one immutable
`program.Program`. Binding validates a temporary `result.Explainer` and the
clause explanation rows needed to distinguish satisfied from false drivers,
then commits atomically. A failed rebind leaves the prior Program usable.

`Append` accepts caller-owned destination bytes, request IDs parallel to the
numeric result rows, the `result.Batch`, and engine-version bytes. It appends one
complete document and a trailing newline. The Encoder owns one reusable
`result.Materialized`; every returned output slice belongs to the caller. The
Encoder is not safe for concurrent use, matching decoder and evaluator worker
ownership.

On failure, `Append` returns the destination at its original logical length.
Bytes beyond that length may have been overwritten and are not part of the
returned value. Binding and row validation never mutate caller data.

## Output Contract

Top-level fields remain in this order:

1. `schema_version`
2. `policy` (`name`, `version`, `sha256`)
3. `engine_version`
4. `results`

Each result preserves the existing order: request ID, decision, rationale,
driver, applied requirements, used evidence, evidence issues, assumptions,
unresolved uncertainty, and remediation. IDs use their stable `R`, `C`, and
`E` prefixes. Evidence issues, assumptions, uncertainty, requirements,
evidence, and remediations retain compiled/result source order.

A zero engine reason is classified by the driver's clause ExplanationID:
branch zero is `satisfied`, branch one is `condition_false`. Nonzero reasons use
the result package's fixed reason names. `add_evidence` remediation emits an
evidence-kind string. `set_field` emits the field and a JSON value preserving
its symbol, integer, Boolean, or timestamp type.

The encoder emits canonical two-space indentation and minimal JSON string
escapes compatible with Go JSON output, including controls, quotes,
backslashes, HTML-sensitive ASCII, U+2028/U+2029, and invalid UTF-8 replacement.

## Validation And Bounds

`Bind` rejects nil, malformed, or explanation-incompatible Programs. `Append`
rejects an unbound/nil encoder, nil or malformed results, row/request-count
mismatches, zero request IDs, a driver count other than one, invalid driver
branch provenance, and empty engine versions. `result.Explainer` remains the
single validator for all row CSR ranges, IDs, authored text bounds, and
structured remediation references.

Rendered text remains bounded at 4,096 bytes per row. Total JSON size is bounded
by the already bounded input row count and caller/service limits; Task 25 adds no
second arbitrary response cap.

## Performance And Verification

The hot path performs one row pass and one scan of each emitted byte range.
There are no maps, reflection, interfaces, callbacks, per-row objects, or
temporary strings. Warm encoding allocates zero bytes when the caller supplies
enough output capacity and the Encoder has been primed.

Tests cover exact golden bytes, deterministic repeat/reuse, prefix append,
escaping, both remediation kinds, zero rows, maximum IDs, malformed input,
failed-bind and failed-append atomicity, 386 safety, and zero warm allocations.
The conformance test switches from its temporary projector to this production
encoder, making both required output files direct encoder artifacts.
