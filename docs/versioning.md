# Versioning

NornRune follows [Semantic Versioning 2.0.0](https://semver.org/) after the
first stable release. Before `v1.0.0`, minor versions may contain breaking
changes and release notes will identify them.

A major version is required for incompatible changes to public Go packages,
CLI commands or flags, environment variables, persisted database identifiers,
the generated API, serialized policy/result formats, decision meanings, or
policy semantics. A minor version adds backward-compatible capabilities. A
patch version contains backward-compatible fixes and documentation changes.

Generated protobuf and frontend API descriptors are part of the compatibility
surface. Policy source hashes identify exact source bytes and may change when
identity or policy text changes even when decisions remain equivalent.

Security support follows [SECURITY.md](../SECURITY.md). No compatibility period
beyond the documented supported-version policy is implied.
