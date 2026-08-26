# NornRune Complete Rename Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Rename the complete Verifoxx codebase and public repository to NornRune without changing policy decisions, evaluator semantics, generated-code determinism, or warmed allocation behavior, then add the missing OSS project surface.

**Architecture:** Perform one clean identity migration with no legacy runtime aliases. Lock the intended new identity and behavioral invariants in tests first, move hand-authored sources with Git history, regenerate authoritative artifacts, and make a repository-wide old-name scanner the final gate. Merge before changing the GitHub URL; rename GitHub and update the separate `verifoxx2` README only after the merged commit passes the complete matrix.

**Tech Stack:** Go 1.27, Cobra, protobuf/gRPC, Buf, PostgreSQL 19, Docker Compose, GitHub Actions, GoReleaser, GitHub CLI, Markdown doc contracts.

---

### Task 1: Lock The Rename And Regression Contracts

**Files:**
- Create: `internal/doccheck/brand_test.go`
- Create: `internal/conformance/rename_test.go`
- Modify: `internal/doccheck/ci_test.go`
- Reference: `docs/plans/2026-08-27-nornrune-complete-rename-design.md`

**Step 1: Write the failing canonical-brand test**

Add `TestCanonicalBrandSurface` in `internal/doccheck/brand_test.go`. Require:

```go
var canonicalBrandFiles = map[string][]string{
	"go.mod":                    {"module github.com/sebishogun/nornrune"},
	"README.md":                 {"# NornRune", "Why NornRune"},
	".goreleaser.yaml":          {"project_name: nornrune", "binary: nornrune"},
	"compose.yaml":              {"name: nornrune", "/nornrune"},
	"api/proto/nornrune/v1/nornrune.proto": {"package nornrune.v1;"},
}
```

Require new paths and reject old command, policy, API, generated, fixture, and
testdata paths. Keep the test data-driven so path additions remain explicit.

**Step 2: Write the failing legacy-name scanner**

Walk tracked source/config/documentation roots without following symlinks. For
text files, reject case-sensitive `Verifoxx`, `verifoxx`, and `VERIFOXX`.
Allow only:

- `Verifoxx_AI_Engineer_Assignment.pdf`, which is binary and never read;
- this implementation plan; and
- `docs/plans/2026-08-27-nornrune-complete-rename-design.md`.

Reject legacy names in file and directory paths separately. Skip `.git` and
generated caches, not generated source committed to the repository.

**Step 3: Lock semantic outcomes independently of branding**

In `internal/conformance/rename_test.go`, evaluate the embedded policy and
assert request decisions remain:

```go
R1 Approve
R2 Reject
R3 Revise
R4 Escalate
R5 Escalate
```

Also compare rationale driver, requirements, evidence, missing/conflicting
evidence, assumptions, unresolved uncertainty, and remediation against the
existing canonical semantic expectations while ignoring only policy name/hash.

**Step 4: Run RED**

Run:

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/doccheck ./internal/conformance
```

Expected: FAIL on old module, paths, brand strings, and missing NornRune paths;
the semantic regression test itself passes.

**Step 5: Commit the contracts**

```bash
timeout 30s git add internal/doccheck/brand_test.go internal/doccheck/ci_test.go internal/conformance/rename_test.go
timeout 30s git commit -m "test: lock NornRune rename contract"
```

### Task 2: Rename The Go Module, Packages, Commands, And Source Imports

**Files:**
- Modify: `go.mod`
- Modify: all tracked `*.go` imports under `api/`, `cmd/`, `frontend/`, `internal/`, `migrations/`, `policies/`, and `testdata/`
- Move: `cmd/verifoxx/` to `cmd/nornrune/`
- Move: `cmd/protoc-gen-verifoxx/` to `cmd/protoc-gen-nornrune/`
- Move: `policies/verifoxx/` to `policies/nornrune/`
- Modify: `.goreleaser.yaml`
- Modify: `Dockerfile`
- Modify: `Makefile`
- Modify: `cmd/devx/cmd/*.go`

**Step 1: Rename paths with Git history**

Use non-interactive `git mv` for the three directories. Rename command-facing
test names and local identifiers where they refer to the product; do not rename
generic semantic types merely because they were authored in this repository.

**Step 2: Change the module and imports in bulk**

Set:

```text
module github.com/sebishogun/nornrune
```

Replace repository-owned import prefixes only:

```text
github.com/sebishogun/verifoxx -> github.com/sebishogun/nornrune
```

Run `gofmt` over every changed Go file. Do not edit `go.sum` manually.

**Step 3: Rename build and release targets**

Change the primary binary, plugin binary, GoReleaser project/build IDs,
linker-variable import path, Docker copy/entrypoint, Make/devx command plans,
tool destinations, and expected executable names to `nornrune` and
`protoc-gen-nornrune`.

**Step 4: Run focused GREEN**

```bash
timeout 180s go test -count=1 -timeout 150s ./cmd/... ./policies/nornrune ./internal/app ./internal/buildinfo
timeout 180s go build -trimpath -o /tmp/opencode/nornrune ./cmd/nornrune
timeout 180s go build -trimpath -o /tmp/opencode/protoc-gen-nornrune ./cmd/protoc-gen-nornrune
timeout 180s go mod tidy -diff
```

Expected: builds and tests pass; tidy prints no diff.

**Step 5: Commit**

```bash
timeout 30s git add go.mod .goreleaser.yaml Dockerfile Makefile cmd frontend internal migrations policies testdata api
timeout 30s git commit -m "refactor: rename Go project to NornRune"
```

### Task 3: Rename Runtime Configuration And Developer Surfaces

**Files:**
- Modify: `internal/config/env.go`
- Modify: `internal/config/*_test.go`
- Modify: `internal/adapters/cli/*.go`
- Modify: `internal/adapters/dap/*.go`
- Modify: `internal/adapters/tui/*.go`
- Modify: `internal/debug/*.go`
- Modify: `cmd/loadgen/`
- Modify: `cmd/devx/`
- Modify: `cli/devx`
- Modify: `.vscode/launch.json`
- Modify: `.gitignore`
- Modify: `compose.yaml`
- Modify: `.dockerignore`

**Step 1: Write failing configuration and CLI tests**

Require only `NORNRUNE_*` environment variables, `.nornrune/` state paths,
`nornrune` help/usage names, `nornrune` socket paths, and NornRune user-agent,
metric, and diagnostic prefixes. Add negative tests proving `VERIFOXX_*` is not
accepted as an alias.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/config ./internal/adapters/cli ./internal/adapters/dap ./internal/adapters/tui ./internal/debug ./cmd/devx/cmd
```

Expected: FAIL on old environment variables, paths, and command names.

**Step 3: Implement the clean runtime rename**

Rename all product configuration constants and test-only helper variables to
`NORNRUNE_*`. Rename `.verifoxx` state, DAP/TUI sockets, generated tool paths,
Compose service/project defaults, health-check executables, image references,
and CLI examples. Do not read old names as fallback.

**Step 4: Run GREEN**

```bash
timeout 180s go test -count=1 -timeout 150s ./internal/config ./internal/adapters/cli ./internal/adapters/dap ./internal/adapters/tui ./internal/debug ./cmd/devx/cmd ./internal/e2e
timeout 60s go run ./cmd/nornrune --help
timeout 60s go run ./cmd/nornrune evaluate
```

Expected: tests pass; help is branded NornRune; evaluation returns the same five
decisions with renamed policy metadata.

**Step 5: Commit**

```bash
timeout 30s git add .dockerignore .gitignore .vscode cli cmd compose.yaml internal
timeout 30s git commit -m "refactor: rename NornRune runtime surfaces"
```

### Task 4: Rename Protobuf And API Namespaces, Then Regenerate

**Files:**
- Move: `api/proto/verifoxx/v1/verifoxx.proto` to `api/proto/nornrune/v1/nornrune.proto`
- Move: `api/gen/verifoxx/v1/` to regenerated `api/gen/nornrune/v1/`
- Modify: `frontend/proto/options.proto`
- Modify: `frontend/proto/plugin.go`
- Modify: `frontend/proto/plugin_test.go`
- Modify: `testdata/frontends/proto/policy.proto`
- Modify: `buf.yaml`
- Modify: `buf.gen.yaml`
- Modify: `buf.frontend.gen.yaml`
- Modify: `internal/adapters/grpcapi/*.go`
- Modify: `internal/adapters/wire/*.go`
- Modify: `docs/api.md`

**Step 1: Write failing descriptor and plugin identity tests**

Require protobuf packages `nornrune.v1`, `nornrune.frontend`, and
`nornrune.frontend.fixture`; Go package paths under
`github.com/sebishogun/nornrune`; plugin name `protoc-gen-nornrune`; and no old
descriptor full names.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend/proto ./internal/adapters/grpcapi ./internal/adapters/wire
```

Expected: FAIL on descriptor names and generated paths.

**Step 3: Rename hand-authored protobuf sources and generator configuration**

Move source paths, update package/go_package declarations, plugin import paths,
Buf module/type selectors, and gRPC adapter imports. Delete obsolete generated
files only after the new source paths exist.

**Step 4: Regenerate through pinned workflows**

```bash
timeout 300s env PATH="/tmp/opencode/verifoxx-tools:$PATH" go run ./cmd/devx proto:gen
timeout 300s newgrp docker <<< 'env PATH="/tmp/opencode/verifoxx-tools:$PATH" go run ./cmd/devx proto:check'
```

If the external tool cache is renamed during Task 3, use its actual NornRune
path consistently. Do not hand-edit `*.pb.go` files.

**Step 5: Run GREEN and commit**

```bash
timeout 180s go test -count=1 -timeout 150s ./frontend/proto ./internal/adapters/grpcapi ./internal/adapters/wire
timeout 30s git diff --check
timeout 30s git add api buf.yaml buf.gen.yaml buf.frontend.gen.yaml frontend/proto internal/adapters/grpcapi internal/adapters/wire testdata/frontends/proto docs/api.md
timeout 30s git commit -m "refactor: rename NornRune protobuf APIs"
```

### Task 5: Rewrite PostgreSQL And Compose Identity

**Files:**
- Modify: `migrations/*.sql`
- Modify: `migrations/*.go`
- Modify: `docker/postgres/init-roles.sh`
- Modify: `compose.yaml`
- Modify: `internal/adapters/postgres/*.go`
- Modify: `internal/persistence/*.go`
- Modify: `internal/server/*.go`
- Modify: `docs/database.md`
- Modify: `docs/operations.md`

**Step 1: Write failing migration identity tests**

Extend migration and integration tests to require database `nornrune`, roles
`nornrune_migrator` and `nornrune_runtime`, schema/graph names under
`nornrune`, `NORNRUNE_MIGRATION_PASSWORD`, `NORNRUNE_RUNTIME_PASSWORD`, and
NornRune application names. Assert migration text contains no legacy identity.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s ./migrations ./internal/adapters/postgres ./internal/persistence ./internal/server
```

Expected: FAIL on old roles, schema, graph, defaults, and environment names.

**Step 3: Rewrite the migration baseline**

Update existing up/down migrations directly. Keep migration ordering and
transactional/immutability semantics unchanged while renaming all SQL objects,
comments, connection defaults, role grants, SQL/PGQ graph references, and test
expectations. There is no upgrade bridge or compatibility view.

**Step 4: Run local and Docker GREEN**

```bash
timeout 180s go test -count=1 -timeout 150s ./migrations ./internal/adapters/postgres ./internal/persistence ./internal/server
timeout 300s newgrp docker <<< 'go test -count=1 -tags=integration -timeout 240s ./internal/adapters/postgres ./internal/server'
```

Expected: clean bootstrap, migration, graph query, publication, audit, and
shutdown tests pass under NornRune identifiers.

**Step 5: Commit**

```bash
timeout 30s git add compose.yaml docker migrations internal/adapters/postgres internal/persistence internal/server docs/database.md docs/operations.md
timeout 30s git commit -m "refactor: rename NornRune persistence"
```

### Task 6: Rename Policies, Fixtures, Results, And Semantic Metadata

**Files:**
- Move: `internal/fixtures/verifoxx-*.json` to `internal/fixtures/nornrune-*.json`
- Move: `policies/verifoxx/` references to `policies/nornrune/`
- Modify: `testdata/policies/*.json`
- Modify: `testdata/golden/*.json`
- Modify: `testdata/frontends/**/*`
- Modify: `results/requests.json`
- Modify: `internal/fixtures/*.go`
- Modify: `internal/conformance/*.go`
- Modify: `internal/benchdata/*.go`

**Step 1: Write failing metadata tests**

Require policy name, fixture pack, frontend fixture identity, embedded paths,
benchmark labels, and canonical result policy identity to be `nornrune`. Keep
requirement IDs and request IDs unchanged.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/fixtures ./internal/conformance ./internal/benchdata ./policies/nornrune
```

Expected: FAIL on old embedded paths and metadata.

**Step 3: Rename and regenerate**

Move fixture files, update embed directives and semantic metadata, then run:

```bash
timeout 300s go run ./cmd/devx policy:check
timeout 300s go run ./cmd/devx results:gen
timeout 300s go run ./cmd/devx results:check
```

Review canonical diffs. Decision/provenance content must remain equivalent;
only NornRune identity and source-derived hashes may differ.

**Step 4: Run GREEN and zero-allocation smoke benchmark**

```bash
timeout 180s go test -count=1 -timeout 150s ./internal/fixtures ./internal/conformance ./internal/benchdata ./policies/nornrune ./internal/eval
timeout 120s go test -timeout 90s -run '^$' -bench '^BenchmarkFrontendWarmEvaluation' -benchmem -benchtime=100x -count=1 ./internal/frontend
```

Expected: tests pass and every warmed case reports `0 B/op`, `0 allocs/op`.

**Step 5: Commit**

```bash
timeout 30s git add internal/fixtures internal/conformance internal/benchdata internal/eval internal/frontend policies/nornrune testdata results
timeout 30s git commit -m "refactor: rename NornRune policy fixtures"
```

### Task 7: Rename Documentation And Explain The Name

**Files:**
- Modify: `README.md`
- Modify: `Requirements.md`
- Modify: `AGENTS.md`
- Modify: all `docs/**/*.md` except the two rename plan allowlist files
- Modify: source comments containing product identity
- Modify: `.github/workflows/*.yml`
- Modify: `internal/doccheck/*.go`

**Step 1: Add the naming section**

Add `## Why NornRune` near the README introduction:

```markdown
## Why NornRune

In Norse tradition, the Norns shape fate across past, present, and future. A
rune is a written symbol and a piece of encoded knowledge. NornRune compiles
written rules and evidence into deterministic, auditable decisions whose
meaning can depend on time, history, and uncertainty.
```

Use the same concise rationale in architecture documentation without duplicating
marketing prose throughout technical guides.

**Step 2: Rename documentation and workflow text**

Replace product identity in prose, examples, links, paths, diagrams, commands,
environment variables, image names, and plan history. Preserve technical facts,
dates, measured numbers, and assignment semantics. Do not alter the source PDF.

**Step 3: Run documentation GREEN**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/doccheck
timeout 30s git diff --check
```

Expected: doc links, commands, workflow timeout contracts, brand surface, and
legacy-name scanner pass.

**Step 4: Commit**

```bash
timeout 30s git add README.md Requirements.md AGENTS.md docs .github internal/doccheck
timeout 30s git commit -m "docs: rename project to NornRune"
```

### Task 8: Add OSS Readiness Surface

**Files:**
- Create: `LICENSE`
- Create: `CONTRIBUTING.md`
- Create: `CODE_OF_CONDUCT.md`
- Create: `SECURITY.md`
- Create: `SUPPORT.md`
- Create: `docs/versioning.md`
- Create: `docs/dependency-licenses.md`
- Create: `.github/ISSUE_TEMPLATE/bug_report.yml`
- Create: `.github/ISSUE_TEMPLATE/feature_request.yml`
- Create: `.github/ISSUE_TEMPLATE/config.yml`
- Create: `.github/PULL_REQUEST_TEMPLATE.md`
- Modify: `README.md`
- Modify: `internal/doccheck/*.go`

**Step 1: Write failing community-health tests**

Require all files above, local links from README, current bounded test commands,
private vulnerability reporting instructions, supported-version policy, DCO or
contribution terms, and no unsupported SLA or security guarantee.

**Step 2: Audit dependency licensing**

Capture every direct module from `go.mod`, its pinned version, license family,
source URL, and compatibility conclusion in `docs/dependency-licenses.md`.
Investigate transitive exceptions surfaced by the release build. Do not infer a
license from package names.

**Step 3: Add Apache-2.0 project licensing and community files**

Use the canonical Apache License 2.0 text and a concise copyright notice. Use
Contributor Covenant 2.1, adapted only for valid contact/reporting information.
Document reproducible setup, coding/testing rules, performance evidence
requirements, generated-file workflow, review expectations, semantic security
boundaries, support limitations, and semantic versioning policy.

**Step 4: Run GREEN**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/doccheck
timeout 180s go mod verify
timeout 30s git diff --check
```

Expected: community-health and documentation contracts pass; module checksums
verify.

**Step 5: Commit**

```bash
timeout 30s git add LICENSE CONTRIBUTING.md CODE_OF_CONDUCT.md SECURITY.md SUPPORT.md README.md docs/versioning.md docs/dependency-licenses.md .github internal/doccheck
timeout 30s git commit -m "docs: add NornRune OSS project files"
```

### Task 9: Prove The Complete Rename And Release Matrix

**Files:**
- Modify as required by failures only
- Verify: all tracked files except the explicit rename-plan/PDF allowlist

**Step 1: Run the forbidden-identity and focused generation gates**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/doccheck ./internal/conformance
timeout 300s env PATH="/tmp/opencode/verifoxx-tools:$PATH" go run ./cmd/devx policy:check
timeout 300s env PATH="/tmp/opencode/verifoxx-tools:$PATH" go run ./cmd/devx results:check
timeout 300s newgrp docker <<< 'env PATH="/tmp/opencode/verifoxx-tools:$PATH" go run ./cmd/devx proto:check'
```

Update the temporary tool path itself to NornRune if Task 3 makes it canonical.

**Step 2: Run static and module gates**

```bash
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 180s go mod tidy -diff
timeout 30s git diff --check
```

Expected: no output or diagnostics.

**Step 3: Run every test lane once**

```bash
timeout 300s go test -count=1 -timeout 240s ./...
timeout 360s go test -count=1 -timeout 300s -race -gcflags=all=-d=checkptr=2 ./...
timeout 300s env GOARCH=386 go test -count=1 -timeout 240s ./...
timeout 300s go test -count=1 -tags=purego -timeout 240s ./...
timeout 420s newgrp docker <<< 'go test -count=1 -tags=integration -timeout 360s ./...'
```

Expected: all lanes pass. Investigate failures; do not rerun a failed lane until
its cause is understood.

**Step 4: Run build, release, and benchmark gates**

```bash
timeout 300s go run ./cmd/devx build
timeout 300s go build -trimpath -o /tmp/opencode/protoc-gen-nornrune ./cmd/protoc-gen-nornrune
timeout 300s go run github.com/goreleaser/goreleaser/v2@v2.12.3 check
timeout 240s go test -timeout 210s -run '^$' -bench '^BenchmarkFrontend' -benchmem -benchtime=100x -count=1 ./internal/frontend
timeout 180s go test -timeout 150s -run '^$' -bench 'BenchmarkExecutor|BenchmarkScheduled' -benchmem -benchtime=100x -count=1 ./internal/eval ./internal/scheduler
```

Expected: release configuration passes; every warmed evaluator benchmark remains
`0 B/op`, `0 allocs/op`.

**Step 5: Request independent review**

Review the full diff against the approved design, emphasizing accidental
semantic edits, stale identity, generated descriptors, database bootstrap,
module resolution, CLI/config names, documentation accuracy, licenses, and
zero-allocation paths. Add focused RED/GREEN tests for every Critical, High, or
Medium finding.

**Step 6: Commit final corrections**

```bash
timeout 30s git add <reviewed-files-only>
timeout 30s git commit -m "fix: complete NornRune rename audit"
```

Skip an empty commit if review requires no changes.

### Task 10: Merge, Rename GitHub, And Update The Secondary Repository

**Files and systems:**
- GitHub: `sebishogun/Verifoxx` -> `sebishogun/nornrune`
- Git remote: `origin`
- Secondary repository: `sebishogun/verifoxx2`, `README.md` only
- GitHub repository description, topics, homepage, branch protection, and social preview if available

**Step 1: Push and merge through a reviewed PR**

Inspect status, full base diff, recent commits, remote tracking, and included
commits. Push `chore/nornrune-rename`, create the PR, wait for checks, and merge
with linear history. Approval count remains zero as already configured; do not
disable pull-request enforcement or other protections.

**Step 2: Verify merged `main` before the URL change**

```bash
timeout 60s git pull --ff-only
timeout 300s go test -count=1 -timeout 240s ./...
timeout 300s env GOARCH=386 go test -count=1 -timeout 240s ./...
```

Expected: merged `main` passes and is clean.

**Step 3: Rename the primary GitHub repository**

Use GitHub's repository rename API to set `name=nornrune`. Then update `origin`
to `https://github.com/sebishogun/nornrune.git`. Verify:

```bash
timeout 30s gh repo view sebishogun/nornrune --json nameWithOwner,url,visibility,defaultBranchRef
timeout 60s git ls-remote https://github.com/sebishogun/nornrune.git refs/heads/main
timeout 60s git ls-remote https://github.com/sebishogun/Verifoxx.git refs/heads/main
timeout 120s GOPROXY=direct go list -m github.com/sebishogun/nornrune@main
```

Expected: the new URL resolves, the old GitHub URL redirects, and the new module
path resolves.

**Step 4: Set OSS repository metadata**

Set description to a concise NornRune policy-engine description, enable issues,
retain public visibility, and add bounded topics such as `policy-engine`,
`authorization`, `policy-as-code`, `go`, `simd`, `postgresql`, and `grpc`.
Verify license detection reports Apache-2.0 and community-health files are
visible.

**Step 5: Update only the link in `verifoxx2`**

Clone `sebishogun/verifoxx2` into `/tmp/opencode/verifoxx2`, inspect its README,
and change the primary-project reference/link from the old URL/name to
`NornRune` and `https://github.com/sebishogun/nornrune`. Do not rename that
repository or alter its implementation. Commit and push the one-file change.

**Step 6: Final remote verification and cleanup**

Verify both repositories are public and live, NornRune CI is green, the
secondary README link resolves, local `main` tracks the renamed origin, and no
feature worktree contains uncommitted changes. Remove the merged worktree and
delete its branch through the normal non-force path.
