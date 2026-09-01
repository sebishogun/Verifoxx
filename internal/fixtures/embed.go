// Package fixtures embeds the baseline conformance input pack. The pack is
// preserved verbatim from the archived source material in
// docs/archive/source-material/ and is the fixed regression corpus for the
// engine.
//
// The three files are read-only inputs to the engine:
//   - nornrune-policy.json holds the three source requirement statements
//     (requirement IDs R1-R3 and exact natural-language text, not the later
//     compiled semantic AST);
//   - nornrune-requests.json holds the five baseline request records
//     (request IDs R1-R5);
//   - nornrune-evidence.json holds the four baseline evidence records
//     (evidence IDs E1-E4).
//
// Ownership contract: accessors return Go strings, which are immutable by
// language contract, so callers cannot mutate package-owned embedded
// storage. A conversion such as []byte(PolicyJSON()) yields caller-owned
// mutable bytes and cannot modify the embedded content; no extra copy is
// required for safety.
package fixtures

import _ "embed"

//go:embed nornrune-policy.json
var policyJSON string

//go:embed nornrune-requests.json
var requestsJSON string

//go:embed nornrune-evidence.json
var evidenceJSON string

// PolicyJSON returns the embedded requirements-source policy fixture.
func PolicyJSON() string { return policyJSON }

// RequestsJSON returns the embedded candidate request pack fixture.
func RequestsJSON() string { return requestsJSON }

// EvidenceJSON returns the embedded candidate evidence pack fixture.
func EvidenceJSON() string { return evidenceJSON }
