# Neovim DAP And Delve Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make `Debug NornRune` automatically available in the installed Neovim/nvim-dap setup and align every repository developer workflow with Delve's DAP protocol.

**Architecture:** Keep `.vscode/launch.json` as the shared target launch contract. Neovim owns an ephemeral loopback Delve process for normal use, while `devx debug` and `debug:dap` remain equivalent fixed-port alternatives. The semantic TUI stays on its independent owner-only Unix socket.

**Tech Stack:** Go 1.27, Delve v1.27.1, Neovim 0.12, nvim-dap, Mason, Cobra, JSON launch configurations, Bubble Tea

---

### Task 1: Remove The Legacy Non-DAP Developer Workflow

**Files:**
- Modify: `cmd/devx/cmd/debug.go`
- Modify: `cmd/devx/cmd/root_test.go`

**Step 1: Write the failing workflow test**

Require both `debug` and `debug:dap` to produce exactly:

```text
dlv dap --listen 127.0.0.1:38697 --log
```

Replace the obsolete test that expects `debug` to inject an absolute semantic
socket into `dlv debug --api-version=2` with an alias-equivalence test.

**Step 2: Run RED**

```bash
timeout 90s go test -count=1 -timeout 60s \
  -run 'TestRepresentativeWorkflowPlansUseExactArguments|TestDebugWorkflowAliasesDAPServer' \
  ./cmd/devx/cmd
```

Expected: FAIL because `debug` still uses Delve's legacy headless API.

**Step 3: Implement the DAP alias**

Return the same bounded `commandSpec` for `debug` and `debug:dap`. Remove target
arguments and repository path rewriting from the fixed DAP server; the client
launch request owns the target.

**Step 4: Run GREEN**

Run the focused command above, then:

```bash
timeout 120s go test -count=1 -timeout 90s ./cmd/devx/cmd ./internal/adapters/dap
```

Expected: PASS.

### Task 2: Load Repository Launch Files In Live Neovim

**Files:**
- Modify: `/home/sebishogun/.config/nvim/lua/plugins/dap.lua`

**Step 1: Preserve current configuration state**

Record `git status --short` in the Neovim configuration repository. Do not edit
its existing dirty files other than `lua/plugins/dap.lua`, and do not update
`lazy-lock.json`.

**Step 2: Establish the missing behavior**

From the NornRune root, run headless Neovim without manually loading launch JSON
and inspect `dap.configurations.go`.

Expected: generic Go configurations exist but `Debug NornRune` does not.

**Step 3: Add bounded project launch discovery**

After defining generic Go configurations, resolve the Go project root, append
`/.vscode/launch.json`, and call:

```lua
if vim.fn.filereadable(launch_json) == 1 then
  require("dap.ext.vscode").load_launchjs(launch_json, { go = { "go" } })
end
```

Also use Delve's explicit `--listen` spelling in the executable adapter. Keep
the existing Mason/system executable selection and all generic configurations.

**Step 4: Verify headless discovery**

Run Neovim from the repository root and require exactly one `Debug NornRune`
configuration with the expected program, cwd, arguments, and build flags.

Expected: PASS with six Go configurations total.

### Task 3: Make The Current Delve Release Available To Repository Tools

**Files:**
- No repository file changes.

**Step 1: Confirm upstream and Mason versions**

Verify that GitHub's current Delve release and Mason's installed receipt both
identify `v1.27.1`.

**Step 2: Install into the configured Go binary directory**

```bash
timeout 240s go install github.com/go-delve/delve/cmd/dlv@v1.27.1
```

**Step 3: Verify tool discovery**

```bash
timeout 10s dlv version
timeout 30s ./cli/devx status
```

Expected: Delve reports 1.27.1 and `debug` plus `debug:dap` are runnable.

### Task 4: Synchronize Debugger Documentation

**Files:**
- Modify: `docs/debugging.md`
- Modify: `README.md`

**Step 1: Update the canonical flow**

Document that normal Neovim use imports `.vscode/launch.json` automatically and
starts an ephemeral Delve server. State that `debug` and `debug:dap` are aliases
for the fixed loopback server alternative. Remove the legacy implication that
`devx debug` directly hosts `debug-worker` before a DAP launch request.

**Step 2: Document the dashboard relationship**

Describe the full-screen terminal overview, AST/Program tabs, Session/Persisted
history, and the fact that a native breakpoint pauses semantic replies.

**Step 3: Run documentation checks**

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/doccheck
timeout 30s git diff --check
```

Expected: PASS.

### Task 5: Verify Native And Semantic Debugging End To End

**Files:**
- Temporary scripts/transcripts only under `/tmp/opencode`.

**Step 1: Run repository verification**

```bash
timeout 180s go test -count=1 -timeout 150s \
  ./cmd/devx/cmd ./internal/adapters/dap ./internal/adapters/cli \
  ./internal/adapters/tui ./internal/debug ./internal/eval
timeout 180s go vet ./cmd/devx/... ./internal/adapters/dap/... \
  ./internal/adapters/cli/... ./internal/adapters/tui/...
```

Expected: PASS.

**Step 2: Launch through real nvim-dap**

Use headless Neovim from the repository root, select `Debug NornRune`, and wait
under a fixed timeout for `.nornrune/debug.sock`. Confirm the DAP initialized and
the worker owns the socket.

**Step 3: Attach the semantic TUI**

Under a pseudo-terminal, enter the alternate screen, switch AST/Program, open
Session/Persisted history, perform one semantic step, and quit. Confirm the
alternate screen is restored.

**Step 4: Terminate and inspect cleanup**

Terminate nvim-dap, confirm Delve and the debug worker exit, and ensure a later
worker can safely replace any stale socket. Do not leave test processes running.

**Step 5: Run the final repository gate**

```bash
timeout 240s go test -count=1 -timeout 180s ./...
timeout 240s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 30s git diff --check
```

Expected: PASS. Do not create a commit unless the user explicitly requests one.
