# NornRune Complete Rename Design

**Date:** 2026-08-27
**Status:** Approved

## Name

The project becomes **NornRune**, written `NornRune` in prose and `nornrune` in
repositories, modules, commands, packages, configuration, and persisted
identifiers.

In Norse tradition, the Norns shape fate across past, present, and future. A
rune is both a written symbol and a piece of encoded knowledge. NornRune fits a
policy engine that compiles written rules and evidence into deterministic,
auditable decisions whose meaning can depend on time, history, and uncertainty.

## Scope

This is a clean, atomic rename. No deprecated `verifoxx` command, module,
environment variable, API namespace, database identifier, image, package, or
configuration alias remains. Existing SQL migrations may be rewritten because
there are no deployed consumers or persisted installations to preserve.

The rename covers:

- GitHub repository, Go module, imports, package aliases, commands, binaries,
  release archives, image names, and build metadata;
- `NornRune`, `nornrune`, and `NORNRUNE` branding and identifiers;
- HTTP examples, protobuf packages and generated bindings, policy and fixture
  names, canonical results, Docker Compose, developer tooling, editor launch
  configuration, local state directories, and Unix sockets;
- PostgreSQL database, schemas, roles, graph names, defaults, scripts, tests,
  and all migration text;
- documentation, roadmap text, CI, doc checks, generated-code checks, and
  repository metadata; and
- the `verifoxx2` README's link to the primary project. The `verifoxx2`
  repository itself remains live and keeps its current name.

The original assignment PDF remains byte-identical as a historical source
artifact. Its filename and embedded wording are the only intentional old-name
allowlist. Git history is not rewritten.

## Regression Contract

The name changes; policy behavior does not. Before editing, record the current
test, generated-file, result, and benchmark gates. During migration, tests lock
the following invariants:

- all five supplied requests retain their decisions, rationales, applied
  requirements, evidence use, uncertainty, and remediation semantics;
- native, CEL, Rego, Cedar, and generated Protobuf policies retain conformance;
- scalar, automatic SIMD, pure-Go, 386, scheduler, debug, HTTP, gRPC, and
  PostgreSQL paths remain semantically equivalent;
- warmed evaluation remains `0 B/op` and `0 allocs/op`;
- policy and result hashes may change only where renamed canonical source bytes
  contribute to the hash;
- generated descriptors and bindings are regenerated, deterministic, and clean
  under drift checks; and
- CLI help, JSON, API descriptors, container health checks, and developer
  workflows consistently expose only NornRune names.

A repository-wide doc check rejects `Verifoxx`, `verifoxx`, and `VERIFOXX` in
tracked text outside the explicit PDF exception and migration design history
needed to describe the old name. Binary and generated-file searches run after
regeneration. This converts the 369-file rename surface into a maintained
contract rather than a one-time search.

## Migration Sequence

1. Add failing rename contracts for canonical names, forbidden legacy names,
   expected path moves, CLI/config identifiers, API/protobuf namespaces,
   database identifiers, generated artifacts, and documentation.
2. Rename the Go module and all imports, then move command, policy, API,
   generated, fixture, and testdata paths with history-preserving file moves.
3. Rename public and operational identifiers: commands, environment variables,
   local state, sockets, Compose resources, images, release configuration, and
   developer workflows.
4. Rewrite PostgreSQL migrations, roles, schemas, database defaults, graph
   names, scripts, integration tests, and documentation as one clean baseline.
5. Rename protobuf source packages and plugin identifiers, then regenerate Go,
   gRPC, and frontend bindings only through pinned generation workflows.
6. Rename policy metadata and fixture packs, regenerate canonical results, and
   compare normalized old/new behavior where branding and source hashes are
   intentionally different.
7. Update all prose, diagrams, examples, source comments, plan files, AI usage
   disclosure, and repository-facing metadata. Add the naming rationale to the
   README and architecture guide.
8. Add OSS readiness: dependency-license audit, selected project license,
   contribution guide, code of conduct, security policy, support policy, issue
   and pull-request templates, release/versioning policy, topics, description,
   and community-health checks.
9. Run focused gates after each layer and the complete release matrix after all
   layers: native, race/checkptr, 386, purego, integration, fuzz seeds, vet,
   field alignment, generation drift, canonical results, builds, containers,
   GoReleaser, benchmarks, and whitespace checks. Every command remains bounded
   by explicit process and test timeouts.
10. Merge while the repository is still named `Verifoxx`. Rename the GitHub
    repository to `nornrune` only after the merged commit is green, update local
    remotes, verify GitHub redirects and `go list` resolution, then update and
    verify the link in `verifoxx2/README.md`.

## Failure Handling

Each layer is independently reviewable and must return to green before the next
layer. Mechanical replacements never touch binary artifacts, `.git`, external
module cache content, or generated files that have an authoritative generator.
Unexpected semantic output changes stop the migration and require a focused
regression test before correction.

The GitHub rename is the final reversible operation. If repository checks or
module resolution fail after it, rename the repository back, restore the remote,
and diagnose from the already-verified merged commit rather than weakening a
test or retaining a partial dual-name state.
