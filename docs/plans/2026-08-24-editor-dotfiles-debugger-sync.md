# Editor Dotfiles Debugger Sync Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Align the maintained cross-editor and standalone LazyVim repositories with the shared Verifoxx DAP launch contract.

**Architecture:** `Zed-Config` remains the live cross-editor source of truth, while `LazyVim-Config` receives the same explicit Delve adapter argument. Project launch behavior remains in `.vscode/launch.json`, which modern nvim-dap and Zed both discover without duplicated global profiles.

**Tech Stack:** Neovim 0.12, nvim-dap, Delve 1.27.1, Zed DAP, Lua, Go installer tests

---

### Task 1: Synchronize The Standalone LazyVim Delve Adapter

**Files:**
- Existing live source: `/home/sebishogun/zed-config/nvim/lua/plugins/dap.lua:133-148`
- Modify: `/home/sebishogun/neovim-configs/LazyvimCustomConfig/lua/plugins/dap.lua:133-148`

**Step 1: Confirm the mismatch**

Run:

```bash
timeout 10s rg -n --fixed-strings 'args = { "dap", "--listen", "127.0.0.1:${port}" }' \
  /home/sebishogun/zed-config/nvim/lua/plugins/dap.lua \
  /home/sebishogun/neovim-configs/LazyvimCustomConfig/lua/plugins/dap.lua
```

Expected: only `Zed-Config` matches.

**Step 2: Apply the minimal standalone change**

Change the standalone adapter from:

```lua
args = { "dap", "-l", "127.0.0.1:${port}" },
```

to:

```lua
args = { "dap", "--listen", "127.0.0.1:${port}" },
```

Do not touch either repository's unrelated changes.

**Step 3: Confirm both maintained configurations match**

Run the Step 1 command again.

Expected: both maintained files match.

### Task 2: Document Cross-Editor Project Launch Discovery

**Files:**
- Modify: `/home/sebishogun/zed-config/README.md`

**Step 1: Add the project debugger contract**

Add a short section stating:

- modern nvim-dap automatically reads `.vscode/launch.json` through its built-in provider;
- Zed imports `.vscode/launch.json` when `.zed/debug.json` is absent;
- VS Code uses the file natively;
- project profiles belong in the project repository, not these user-level dotfiles;
- the shared `space d ...` controls operate on whichever profile the editor loaded.

**Step 2: Check formatting**

Run:

```bash
timeout 30s git diff --check
```

Expected: PASS in `Zed-Config`.

### Task 3: Verify Both Repositories And The Live Launch Path

**Files:**
- Verify only: `/home/sebishogun/zed-config/nvim/lua/plugins/dap.lua`
- Verify only: `/home/sebishogun/neovim-configs/LazyvimCustomConfig/lua/plugins/dap.lua`
- Verify only: `/home/sebishogun/Learning/Go/Verifoxx/.worktrees/main/.vscode/launch.json`

**Step 1: Confirm the maintained DAP files remain identical**

```bash
timeout 10s cmp -s \
  /home/sebishogun/zed-config/nvim/lua/plugins/dap.lua \
  /home/sebishogun/neovim-configs/LazyvimCustomConfig/lua/plugins/dap.lua
```

Expected: PASS. This proves no non-Go adapter drift was introduced between the
maintained configurations.

**Step 2: Syntax-check both Lua files**

```bash
timeout 10s luac -p /home/sebishogun/zed-config/nvim/lua/plugins/dap.lua
timeout 10s luac -p /home/sebishogun/neovim-configs/LazyvimCustomConfig/lua/plugins/dap.lua
```

Expected: PASS.

**Step 3: Run the cross-editor installer tests**

```bash
timeout 90s go test -count=1 -timeout 60s ./...
```

Expected: PASS from `/home/sebishogun/zed-config`.

**Step 4: Verify the complete live adapter surface**

Use headless Neovim to assert:

- the Go adapter resolves `dlv dap --listen 127.0.0.1:${port}`;
- CodeLLDB remains registered for Rust, C, C++, and Zig;
- debugpy remains registered for Python;
- vscode-js-debug remains registered for JavaScript and TypeScript;
- Java launch/attach configurations remain present;
- DAP UI lifecycle listeners and the `space d ...` keymaps remain present.

Also confirm the Mason Delve and CodeLLDB executables, debugpy interpreter, and
vscode-js-debug server entry point exist and are runnable under bounded probes.

Expected: every pre-existing adapter family remains available.

**Step 5: Verify nvim-dap project discovery**

From the Verifoxx worktree, query `dap.providers.configs["dap.launch.json"]`
under headless Neovim and assert exactly one `Debug Verifoxx` entry.

Expected: PASS without a deprecated `load_launchjs` warning.

**Step 6: Rerun the real DAP/TUI integration harness**

```bash
timeout 130s /tmp/opencode/verifoxx-nvim-dap-e2e.sh
```

Expected: DAP initialization, semantic TUI navigation and step, alternate-screen
restoration, and clean Delve/worker shutdown.

**Step 7: Inspect scoped diffs**

Confirm `Zed-Config` changes only include its pre-existing user work, the Delve
argument, and README addition. Confirm `LazyVim-Config` changes only include the
Delve argument. Do not create a commit unless the user explicitly requests one.
