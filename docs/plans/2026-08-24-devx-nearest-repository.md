# Nearest-Repository devx Dispatcher Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the repository-pinned global `devx` symlink with a stable dispatcher that runs the nearest ancestor repository's `cli/devx`.

**Architecture:** Keep `cli/devx` as the repository-local host-binary launcher. Add a standalone POSIX dispatcher copied into `$prefix/bin/devx`; it walks upward from the physical current directory and `exec`s the first executable `cli/devx`. Teach the installer to migrate legacy `*/cli/devx` symlinks while refusing unrelated paths.

**Tech Stack:** POSIX shell, Go's `testing` package, `os/exec`, filesystem fixtures, and the existing devx installer tests.

---

### Task 1: Nearest-Repository Dispatcher

**Files:**
- Create: `cli/devx-dispatch`
- Modify: `cmd/devx/cmd/install_test.go`

**Step 1: Write failing dispatcher tests**

Add tests that copy the future dispatcher into a temporary global bin directory and construct executable fake wrappers under outer and nested repositories. Run from a nested child directory and assert:

- the nested repository wins over the outer repository;
- argv containing spaces, shell metacharacters, and empty values reaches the wrapper unchanged;
- the wrapper's exit status is preserved;
- an explicit `DEVX_START_DIRECTORY` is resolved physically; and
- a directory with no ancestor `cli/devx` exits non-zero with `devx: no devx-enabled repository found`.

Use `exec.CommandContext` with a ten-second deadline. Do not invoke a shell around the dispatcher.

**Step 2: Run tests and verify RED**

```bash
timeout 90s go test -count=1 -timeout 60s ./cmd/devx/cmd -run '^TestGlobalDevxDispatcher'
```

Expected: FAIL because `cli/devx-dispatch` does not exist.

**Step 3: Implement the minimal dispatcher**

Create an executable POSIX script beginning with the ownership marker:

```sh
#!/bin/sh
# devx-repository-dispatch-v1
set -eu
```

Resolve `${DEVX_START_DIRECTORY:-$PWD}` with `cd -P`, reject a non-directory start, and walk parents until `/`. For each directory, test `<directory>/cli/devx` with `-f` and `-x`; on the first match, use `exec "$candidate" "$@"`. If no candidate exists, print one diagnostic naming the start directory and exit 1.

**Step 4: Run focused tests and shell syntax**

```bash
timeout 90s go test -count=1 -timeout 60s ./cmd/devx/cmd -run '^TestGlobalDevxDispatcher'
timeout 30s sh -n cli/devx-dispatch
```

Expected: PASS.

### Task 2: Safe Installer Migration

**Files:**
- Modify: `cli/install.sh`
- Modify: `cmd/devx/cmd/install_test.go`
- Modify: `docs/plans/2026-08-20-nornrune-policy-engine-design.md`

**Step 1: Rewrite installer expectations to fail**

Change existing tests from symlink expectations to an executable regular file whose bytes equal `cli/devx-dispatch`. Add cases for:

- dry-run copy planning;
- idempotent update of a marked dispatcher;
- migration of a legacy symlink targeting `/some/repository/cli/devx`;
- uninstall of a marked dispatcher;
- refusal to replace or remove an unrelated file or arbitrary symlink; and
- unchanged `.bashrc` and `.zshrc` sentinels.

**Step 2: Run installer tests and verify RED**

```bash
timeout 90s go test -count=1 -timeout 60s ./cmd/devx/cmd -run '^TestInstaller'
```

Expected: FAIL because the installer still plans and creates a repository symlink.

**Step 3: Implement copy, ownership, and migration rules**

Set `dispatcher=$script_directory/devx-dispatch`. Recognize ownership when a regular destination's second line is exactly `# devx-repository-dispatch-v1`. Recognize a legacy link only when `readlink` matches `*/cli/devx`. Refuse every other existing destination.

For installation, create `$bin_directory`, copy to `$destination.tmp.$$`, set mode `0755`, then rename over an owned dispatcher or legacy link. Trap removal of the temporary path. For uninstall, remove only an owned dispatcher or recognized legacy link. Keep dry-run output exact and leave shell startup files untouched.

**Step 4: Update architecture wording**

Replace the claim that installation creates a repository symlink with the copied nearest-repository dispatcher behavior and explicit no-repository failure.

**Step 5: Run installer, wrapper, and full devx tests**

```bash
timeout 90s go test -count=1 -timeout 60s ./cmd/devx/cmd
timeout 30s sh -n cli/devx cli/devx-dispatch cli/install.sh
timeout 30s ./cli/install.sh --dry-run
```

Expected: PASS; dry-run plans a dispatcher copy, not a symlink.

### Task 3: Live Migration And Repository Gates

**Files:**
- Review all Task 59 files; do not modify or stage `AGENTS.md`.

**Step 1: Migrate the installed command**

```bash
timeout 30s ./cli/install.sh --prefix "$HOME/.local"
```

Expected: the recognized Muzz legacy symlink is replaced by an executable marked dispatcher at `~/.local/bin/devx`.

**Step 2: Verify nearest selection on the machine**

From the NornRune worktree:

```bash
timeout 30s devx --print-host-binary
```

Expected: a path under the current NornRune worktree.

From Muzz:

```bash
timeout 30s devx --help
```

Expected: Muzz's command inventory, proving the same global command selected Muzz there.

From `/tmp/opencode`, where no ancestor implements devx:

```bash
timeout 30s devx --help
```

Expected: non-zero with the no-repository diagnostic.

**Step 3: Run repository gates**

```bash
timeout 180s go test -count=1 -timeout 60s ./...
timeout 180s go vet ./...
timeout 30s git diff --check
```

Expected: PASS.

**Step 4: Commit and push**

```bash
git add cli/devx-dispatch cli/install.sh cmd/devx/cmd/install_test.go docs/plans/2026-08-20-nornrune-policy-engine-design.md docs/plans/2026-08-24-devx-nearest-repository.md
git commit -m "fix: dispatch devx to nearest repository"
git push
```
