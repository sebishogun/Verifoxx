# Debugging

Verifoxx exposes two independent debugger planes:

```text
Neovim nvim-dap -> DAP -> Delve -> Verifoxx debug process
Bubble Tea TUI  -> Unix socket -> retained semantic session
```

Neovim is the only DAP client. Delve's DAP server accepts one client, so the TUI
never connects to that port. It uses the bounded local protocol in
`internal/debug` instead. A native breakpoint pauses the process and therefore
also pauses semantic-socket replies until Neovim resumes it.

## Prerequisites

Install a Delve release compatible with Go 1.27 and confirm it is on `PATH`:

```bash
timeout 180s go install github.com/go-delve/delve/cmd/dlv@v1.27.1
timeout 10s dlv version
```

The DAP launcher always binds IPv4 loopback. Delve's same-user check remains
enabled. Do not expose the DAP listener outside the host.

## Debug Build

Build with the `debug` tag and disable optimization and inlining:

```bash
timeout 120s go build -gcflags=all='-N -l' -tags=debug ./cmd/verifoxx
```

The `debug` tag selects the non-inlined
`internal/debug/debugtrap.Reached(nodeID, instructionID)` hook. It marks the
boundary immediately before a retained semantic instruction executes, so both
typed IDs are visible in Delve. Normal batch evaluation intentionally does not
enter this hook. Release builds select an inlineable empty implementation; the
compiler removes that call path.

Debug binaries are for inspection only. Never use `-tags=debug` or `-N -l` for
benchmarks or performance claims.

## Neovim

The repository's `.vscode/launch.json` is shared by VS Code and nvim-dap. It
launches `verifoxx debug-worker` from the repository root with the required
build tag and compiler flags. The worker compiles the embedded policy and batch,
then listens at `.verifoxx/debug.sock` for one semantic TUI client at a time.

Register Delve and load that file from the repository-local Neovim setup:

```lua
local dap = require("dap")

dap.adapters.go = {
  type = "server",
  port = "${port}",
  executable = {
    command = "dlv",
    args = { "dap", "--listen", "127.0.0.1:${port}" },
    detached = vim.fn.has("win32") == 0,
  },
}

require("dap.ext.vscode").load_launchjs(nil, { go = { "go" } })
```

Open the repository as Neovim's working directory, run `:DapContinue`, and
select `Debug Verifoxx`. Source breakpoints work in every debug launch. To stop
at semantic instruction boundaries, set a breakpoint inside
`debugtrap.Reached`, connect the TUI to `.verifoxx/debug.sock`, and issue a step
or continue command. The current node and instruction IDs are the function
arguments.

## Programmatic Launcher

`internal/adapters/dap.Launcher` is the context-bound launcher used by local
development tooling. `Plan` validates and owns target arguments, chooses an
ephemeral loopback port when `Config.Port` is zero, and returns both the exact
`dlv dap` command and the DAP launch configuration. `Launch` starts the process;
canceling its context interrupts Delve so it can terminate its debuggee and
remove `__debug_bin`, then force-kills Delve if shutdown exceeds two seconds.
`Config.Stdout` and `Config.Stderr` expose Delve diagnostics when needed. A
caller that stops the process directly must call `Server.Close`.

Use a fixed port only when another process has reserved it operationally. The
automatic selector releases its temporary listener before Delve binds, so a
concurrent local process can win that port; retry the complete launch rather
than changing the listener to a non-loopback address.

## Semantic TUI

The Bubble Tea model accepts an `internal/debug.Client`, which dials the Unix
socket exposed by `internal/debug.Server`. That channel carries semantic steps,
breakpoints, watches, replay, evidence, and retained state. It does not carry
DAP frames and must not be configured with the Delve TCP address.

`debug-worker` creates a missing socket directory with mode `0700`, requires an
existing directory to already be owner-only, and sets the socket to `0600`. It
rejects relative paths, existing non-socket files, and sockets with an active
listener. Delve can terminate the worker without running Go defers, so the next
worker probes an existing socket and removes it only when no listener remains.
The probe and removal compare the socket inode to avoid replacing a concurrently
started worker's endpoint.

Keep the Unix socket in a user-owned directory with owner-only permissions.
Closing a TUI connection does not destroy the retained session; a later local
client may reconnect. Cancel the session host when the debug run is complete.
