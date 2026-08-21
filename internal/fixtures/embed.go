// Package fixtures embeds the candidate-exercise input pack exactly as
// transcribed from Verifoxx_AI_Engineer_Assignment.pdf, which is the source
// of truth for these inputs.
//
// The three files are read-only inputs to the engine:
//   - verifoxx-policy.json holds the three source requirement statements
//     (requirement IDs R1-R3 and exact natural-language text, not the later
//     compiled semantic AST);
//   - verifoxx-requests.json holds the five candidate request records
//     (request IDs R1-R5);
//   - verifoxx-evidence.json holds the four candidate evidence records
//     (evidence IDs E1-E4).
//
// Ownership contract: accessors return Go strings, which are immutable by
// language contract, so callers cannot mutate package-owned embedded
// storage. A conversion such as []byte(PolicyJSON()) yields caller-owned
// mutable bytes and cannot modify the embedded content; no extra copy is
// required for safety.
package fixtures

import _ "embed"

//go:embed verifoxx-policy.json
var policyJSON string

//go:embed verifoxx-requests.json
var requestsJSON string

//go:embed verifoxx-evidence.json
var evidenceJSON string

// PolicyJSON returns the embedded requirements-source policy fixture.
func PolicyJSON() string { return policyJSON }

// RequestsJSON returns the embedded candidate request pack fixture.
func RequestsJSON() string { return requestsJSON }

// EvidenceJSON returns the embedded candidate evidence pack fixture.
func EvidenceJSON() string { return evidenceJSON }
