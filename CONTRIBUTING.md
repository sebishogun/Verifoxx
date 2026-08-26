# Contributing to NornRune

Contributions are welcome through GitHub issues and pull requests. Keep changes
focused, explain the behavior being changed, and add tests before production
code for fixes and features.

## Development

Use Go 1.27 and run commands from the repository root. Build generated code
through the repository workflows rather than editing it directly:

```bash
timeout 120s go test -count=1 -timeout 60s ./...
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 300s go run ./cmd/devx proto:gen
timeout 300s go run ./cmd/devx proto:check
```

Docker is required for the pinned protobuf drift check and integration tests.
Every test, benchmark, build, and generation command must have an outer process
deadline; Go tests must also set `-timeout`.

## Policy And Performance Changes

Policy changes must preserve the four decisions and document their effects on
evidence, escalation, and non-negotiable conditions. Update
`results/requests.json` and `testdata/golden/requests.json` when an intentional
policy identity or result change occurs.

Per-node, per-row, and per-request paths must avoid allocation. Include
`-benchmem` evidence for performance-sensitive changes; warmed evaluator cases
must remain `0 B/op`, `0 allocs/op`. Run the field-alignment gate when changing
production structs.

## Pull Requests

Describe the problem, the bounded solution, tests run, generated files changed,
and user-visible compatibility impact. Reviewers may request smaller commits or
additional semantic, race, architecture, or benchmark coverage.

## Developer Certificate of Origin

Contributions use the [Developer Certificate of Origin 1.1](https://developercertificate.org/).
Sign each commit with `git commit -s` to certify that you have the right to
submit the contribution under this project's license.
