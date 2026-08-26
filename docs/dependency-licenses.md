# Dependency Licenses

This audit covers every direct module pinned in `go.mod` on 2026-08-27. License
families were read from each module's distributed license file. Apache-2.0,
MIT, and BSD-3-Clause permit use and redistribution in this Apache-2.0 project,
subject to their notice and redistribution terms.

| Module | Version | License | Source | Conclusion |
| --- | --- | --- | --- | --- |
| `cel.dev/cel-go` | `v0.32.0` | Apache-2.0 | [source](https://github.com/google/cel-go) | Compatible |
| `github.com/cedar-policy/cedar-go` | `v1.8.0` | Apache-2.0 | [source](https://github.com/cedar-policy/cedar-go) | Compatible |
| `github.com/charmbracelet/bubbletea` | `v1.3.10` | MIT | [source](https://github.com/charmbracelet/bubbletea) | Compatible |
| `github.com/charmbracelet/huh` | `v1.0.0` | MIT | [source](https://github.com/charmbracelet/huh) | Compatible |
| `github.com/charmbracelet/lipgloss` | `v1.1.0` | MIT | [source](https://github.com/charmbracelet/lipgloss) | Compatible |
| `github.com/charmbracelet/x/ansi` | `v0.10.1` | MIT | [source](https://github.com/charmbracelet/x) | Compatible |
| `github.com/jackc/pgx/v5` | `v5.10.0` | MIT | [source](https://github.com/jackc/pgx) | Compatible |
| `github.com/mattn/go-runewidth` | `v0.0.19` | MIT | [source](https://github.com/mattn/go-runewidth) | Compatible |
| `github.com/muesli/termenv` | `v0.16.0` | MIT | [source](https://github.com/muesli/termenv) | Compatible |
| `github.com/open-policy-agent/opa` | `v1.19.1` | Apache-2.0 | [source](https://github.com/open-policy-agent/opa) | Compatible |
| `github.com/prometheus/client_golang` | `v1.24.0` | Apache-2.0 | [source](https://github.com/prometheus/client_golang) | Compatible |
| `github.com/sebishogun/simd` | `v1.21.0` | MIT | [source](https://github.com/sebishogun/simd) | Compatible |
| `github.com/spf13/cobra` | `v1.10.2` | Apache-2.0 | [source](https://github.com/spf13/cobra) | Compatible |
| `github.com/testcontainers/testcontainers-go` | `v0.44.0` | MIT | [source](https://github.com/testcontainers/testcontainers-go) | Compatible |
| `github.com/testcontainers/testcontainers-go/modules/postgres` | `v0.44.0` | MIT | [source](https://github.com/testcontainers/testcontainers-go) | Compatible; same project license |
| `google.golang.org/grpc` | `v1.83.1` | Apache-2.0 | [source](https://github.com/grpc/grpc-go) | Compatible |
| `google.golang.org/protobuf` | `v1.36.12` | BSD-3-Clause | [source](https://github.com/protocolbuffers/protobuf-go) | Compatible |

Transitive dependencies are resolved and checksum-pinned by `go.mod` and
`go.sum`. Release changes that add or update dependencies must rerun a complete
license scan and investigate any license outside Apache-2.0, MIT, BSD, or ISC
before distribution. This inventory is engineering documentation, not legal
advice.

The release archive includes the project `LICENSE` and `NOTICE`. The notice
preserves attribution text distributed by direct Apache-2.0 dependencies.
