# Nearest-Repository devx Dispatcher Design

## Goal

Make the globally installed `devx` command execute the closest ancestor
repository that provides an executable `cli/devx`. It must never silently run a
different repository when invoked outside a devx-enabled tree.

## Architecture

Repository-local `cli/devx` remains responsible for selecting its host binary
or falling back to `go run ./cmd/devx`. A new POSIX `cli/devx-dispatch` is the
repository-neutral global entry point. Starting at the physical current working
directory, it walks one parent at a time, executes the first executable
`cli/devx`, and forwards argv unchanged. Reaching the filesystem root without a
match is an error.

`cli/install.sh` copies the dispatcher to `$prefix/bin/devx` instead of linking
the current repository wrapper. A stable marker identifies dispatcher copies
owned by the installer. Install and uninstall may update or remove only a
marked dispatcher or a legacy symlink whose target has the `*/cli/devx` shape;
all unrelated files and links remain protected. Installation uses a temporary
file plus rename so the command is never partially written.

## Behavior

- The nearest ancestor wins, including a repository nested inside another.
- Arguments and exit status pass through unchanged via `exec`.
- Missing, non-directory, or unsupported starting locations fail with a bounded
  diagnostic and do not fall back to Muzz or any other repository.
- `DEVX_START_DIRECTORY` may override the starting directory for tests and
  explicit automation; normal use relies on `$PWD`.
- Shell startup files remain untouched.

## Verification

Tests cover nearest and nested selection, exact argument forwarding,
no-repository failure, dry-run output, installation as a regular executable,
safe update and uninstall, migration of the current legacy Muzz-style symlink,
refusal to replace unrelated destinations, and preservation of shell startup
files. The installed command is then run from this worktree and from
Muzz to prove each resolves its own repository.
