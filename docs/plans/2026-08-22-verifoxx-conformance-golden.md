# Verifoxx Conformance And Golden Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Evaluate the supplied five-request pack end to end and lock canonical assignment-facing JSON with decisions Approve, Reject, Revise, Escalate, Escalate.

**Architecture:** Add optional evidence subject/scope/timing symbols to the existing pointerless expression pipeline, then use one conformance test to decode, compile, decode the batch, execute, project, and compare golden JSON. Keep projection test-local until Task 25.

**Tech Stack:** Go 1.27, existing AST/compiler/evaluator, direct JSON adapters, `encoding/json` only in conformance tests.

---

### Task 1: Carry Evidence Qualifiers Through Compilation

**Files:**
- Modify: `internal/ast/document.go`
- Modify: `internal/ast/builder.go`
- Modify: `internal/adapters/jsonpolicy/expression.go`
- Modify: `internal/program/program.go`
- Modify: `internal/program/freeze.go`
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/normalize_instructions.go`
- Modify: `internal/compile/schedule.go`
- Modify: `internal/eval/executor.go`
- Test: `internal/adapters/jsonpolicy/expression_test.go`

**Step 1: Write one failing expression test**

Decode `evidence_matches` with `subject`, `scope`, and `timing`; compile it and assert all three Program symbol columns are nonzero and resolve to the supplied strings.

**Step 2: Verify RED**

Run: `go test -count=1 -timeout 60s ./internal/adapters/jsonpolicy -run '^TestDecodeEvidenceMatchQualifiers$'`

Expected: FAIL because the keys are rejected.

**Step 3: Implement the pointerless columns**

Add optional `schema.ValueID` AST columns and a qualified evidence builder/accessor while retaining the existing unconstrained helper. Parse optional string keys, lower them through `symbolRemap`, include them in CSE hashing/equality, schedule permutation, Program freezing/validation, and pass them to `eval.EvidencePredicate`. Zero remains unconstrained.

**Step 4: Verify and commit**

Run: `go test -count=1 -timeout 60s ./internal/ast ./internal/adapters/jsonpolicy ./internal/compile ./internal/program ./internal/eval`

Commit: `feat: compile evidence qualifiers`

### Task 2: Add End-To-End Conformance And Golden JSON

**Files:**
- Create: `policies/verifoxx/policy.json`
- Create: `internal/conformance/verifoxx_test.go`
- Create: `results/requests.json`
- Create: `testdata/golden/requests.json`

**Step 1: Write the policy and failing conformance test**

Build the seven symbolic request fields, decode/compile the policy, decode the exact embedded request/evidence fixtures, execute five rows, and require:

```text
R1 Approve  [R1,R2]
R2 Reject   [R1,R2]
R3 Revise   [R2,R3]
R4 Escalate [R1,R2]
R5 Escalate [R2,R3]
```

Project outcome/driver/reason/remediation and request evidence CSR IDs into all assignment-facing fields without switching on request ID. Compare indented canonical JSON with both output files.

**Step 2: Verify RED**

Run: `go test -count=1 -timeout 60s ./internal/conformance`

Expected: FAIL until the policy and golden projection agree.

**Step 3: Correct semantic policy data only**

Use separate R3 clauses for non-negotiable approval/disclosure and revisable usage adjustment. Do not add request-ID branches.

**Step 4: Verify, commit, and complete Task 17**

Run:

```bash
go test -count=1 -timeout 60s ./internal/conformance
go test -count=1 -timeout 60s ./...
timeout 30s gofmt -l .
git diff --check
```

Commit: `feat: add verifoxx policy conformance`
