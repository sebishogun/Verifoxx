# Editor Dotfiles Debugger Sync Design

## Goal

Keep the maintained cross-editor and standalone LazyVim configurations aligned
with the repository's shared DAP launch contract without adding project-specific
global profiles.

## Sources Of Truth

`~/zed-config` is the cross-editor source of truth. Its `nvim` directory is the
live `~/.config/nvim` target, and its Zed settings and keymap are linked into the
live Zed configuration. `~/neovim-configs/LazyvimCustomConfig` is the maintained
standalone `LazyVim-Config` checkout. The older `NVIM-setup` repository and
duplicate clones are outside this synchronization scope.

## Behavior

Both maintained Neovim configurations launch Delve as
`dlv dap --listen 127.0.0.1:${port}`. Current nvim-dap discovers
`.vscode/launch.json` through its built-in configuration provider, so neither
configuration calls the deprecated `dap.ext.vscode.load_launchjs` API.

Zed already imports `.vscode/launch.json` when a project does not define
`.zed/debug.json`. NornRune therefore keeps one repository launch definition for
Neovim, Zed, and VS Code rather than duplicating it in user-level dotfiles. The
existing Zed debugger keybindings remain unchanged.

## Verification

Run the real headless nvim-dap launch against the live canonical configuration,
syntax-check the standalone LazyVim DAP file, run the `Zed-Config` installer
tests, and confirm both maintained files use the explicit Delve argument. The
headless gate must also retain the existing CodeLLDB, debugpy, vscode-js-debug,
and Java configurations, DAP UI hooks, and debugger keymaps, while executable
discovery confirms the installed adapters remain available. Do not modify
unrelated dirty files or create a commit unless requested.
