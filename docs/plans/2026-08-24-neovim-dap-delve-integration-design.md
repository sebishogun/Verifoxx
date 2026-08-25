# Neovim DAP And Delve Integration Design

**Status:** Approved

**Date:** 2026-08-24

## Goal

Make the repository's native-debug workflow directly usable from the installed
Neovim/nvim-dap setup while preserving the semantic TUI as a separate local
debugger plane. Keep `.vscode/launch.json` as the portable launch contract and
use the current Delve release supported by Go 1.27.

## Architecture

Neovim owns the normal Delve process. Its existing Go adapter launches an
ephemeral loopback `dlv dap` server and imports the repository's
`.vscode/launch.json`. The `Debug Verifoxx` launch builds with the `debug` tag
and `-N -l`, starts `debug-worker`, and exposes `.verifoxx/debug.sock`. The
Bubble Tea TUI connects only to that Unix socket and never consumes the Delve
DAP connection.

The repository's fixed-port `debug:dap` workflow remains an explicit alternative
for clients that connect to `127.0.0.1:38697`. The generic `debug` workflow must
use the same DAP protocol rather than the stale legacy headless JSON-RPC command.

## Components

- `.vscode/launch.json` remains the shared VS Code/nvim-dap launch definition.
- `~/.config/nvim/lua/plugins/dap.lua` loads `.vscode/launch.json` when the
  current project provides it, without replacing generic Go configurations.
- Neovim continues to resolve Delve through Mason on Linux. Delve `v1.27.1` is
  also installed into the configured `GOBIN` so repository `devx` workflows can
  resolve `dlv` through `PATH`.
- `cmd/devx/cmd/debug.go`, its tests, and debugger documentation describe DAP
  consistently.

## Failure Handling

Missing project launch files are a no-op in Neovim. Missing Delve keeps the
repository workflow blocked with its existing explicit status reason. DAP binds
IPv4 loopback only and retains Delve's same-user checks. A native breakpoint can
pause semantic-socket replies; the TUI reports the transport state and resumes
once Neovim continues the process.

## Verification

Tests cover exact Delve DAP argv and the shared launch JSON. Headless Neovim must
load `Debug Verifoxx` in addition to generic Go sessions. A real nvim-dap launch
must create the semantic socket, accept the TUI client, support a semantic step,
and terminate without leaving the debug process or alternate screen active.
Unrelated existing Neovim configuration and lockfile changes remain untouched.
