# devx Packaging

Build host binaries and install the global dispatcher:

```sh
timeout 180s ./cli/build.sh --host-only
./cli/install.sh --dry-run
./cli/install.sh
```

The installer creates `~/.local/bin/devx` by default. Override the prefix with
`--prefix DIR` or `DEVX_PREFIX`; the command-line flag wins. It reports when the
selected `bin` directory is absent from `PATH` and does not edit shell startup
files. From any nested directory, the installed command runs the closest
ancestor repository with an executable `cli/devx`; outside such a repository it
fails instead of selecting a default project. Installation migrates legacy
links ending in `cli/devx`. Installation and removal refuse any other existing
path; choose another prefix rather than replacing an unrelated `devx`.

Uninstall only the marked dispatcher or a recognized legacy link:

```sh
./cli/install.sh --uninstall
```

`cli/devx-dispatch` performs global repository selection. The repository-local
`cli/devx` selects `bin/devx-<goos>-<goarch>` when present and otherwise falls
back to `go run ./cmd/devx`. Use `cli/devx --print-host-binary` to inspect host
selection without running a workflow.

Without `--host-only`, `build.sh` cross-builds Linux, macOS, and Windows for
amd64 and arm64. Every binary uses `CGO_ENABLED=0`, `-trimpath`, and stripped
release linker flags.
