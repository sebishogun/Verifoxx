# devx Packaging

Build host binaries and install the repository wrapper:

```sh
timeout 180s ./cli/build.sh --host-only
./cli/install.sh --dry-run
./cli/install.sh
```

The installer creates `~/.local/bin/devx` by default. Override the prefix with
`--prefix DIR` or `DEVX_PREFIX`; the command-line flag wins. It reports when the
selected `bin` directory is absent from `PATH` and does not edit shell startup
files. Installation and removal refuse any existing path not owned by this
repository; choose another prefix rather than replacing an unrelated `devx`.

Uninstall only the symlink owned by this repository:

```sh
./cli/install.sh --uninstall
```

`cli/devx` resolves installation symlinks, selects
`bin/devx-<goos>-<goarch>` when present, and otherwise falls back to
`go run ./cmd/devx`. Use `cli/devx --print-host-binary` to inspect host
selection without running a workflow.

Without `--host-only`, `build.sh` cross-builds Linux, macOS, and Windows for
amd64 and arm64. Every binary uses `CGO_ENABLED=0`, `-trimpath`, and stripped
release linker flags.
