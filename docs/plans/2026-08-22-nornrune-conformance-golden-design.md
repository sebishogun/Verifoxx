# NornRune Conformance And Golden Design

**Date:** 2026-08-22

**Status:** Approved

## Goal

Exercise the complete semantic path over the five supplied requests: decode a
bounded policy, compile it, decode the assignment request/evidence pack,
execute it, and compare assignment-facing machine-readable results. Expected
decisions are Approve, Reject, Revise, Escalate, and Escalate.

## Policy Model

The policy contains the three supplied requirements. R3 uses separate clauses
for non-negotiable disclosure/pre-execution approval and revisable usage-limit
approval, so a missing usage adjustment can produce Revise without weakening a
condition that requires Reject or Escalate. Outcome precedence remains policy
owned.

`evidence_matches` gains optional `subject`, `scope`, and `timing` symbol
constraints. They flow through pointerless AST and Program columns into the
existing `eval.EvidencePredicate`; zero means unconstrained. This represents
the supplied local-environment subject, trusted-internal scope, and
before-execution timing without request-specific branches.

## Conformance Path

One test builds the seven-field symbolic schema, decodes and compiles
`policies/nornrune/policy.json`, decodes the exact embedded request/evidence
fixtures, and runs `eval.Executor`. It asserts decisions and applied
requirement IDs for all five rows.

A test-local projector converts outcome, driver, reason, remediation, and
request evidence CSR IDs into the assignment-facing fields. It branches on
semantic driver IDs, never request IDs. Canonical JSON must match both
`results/requests.json` and `testdata/golden/requests.json`. Task 25 will replace
the projector with the production encoder without changing this contract.

## Verification

Use one failing end-to-end conformance test, focused qualifier tests only where
the current representation cannot satisfy it, then one package run and one
fresh repository run. Broader transport, explanation, and encoder testing stays
in later roadmap tasks.
