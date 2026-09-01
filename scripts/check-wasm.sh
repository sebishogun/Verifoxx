#!/bin/sh
set -eu

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

GOCACHE="$tmp/cache-first" "$(dirname "$0")/build-wasm.sh" "$tmp/first.wasm"
GOCACHE="$tmp/cache-second" "$(dirname "$0")/build-wasm.sh" "$tmp/second.wasm"

if ! cmp -s "$tmp/first.wasm" "$tmp/second.wasm"; then
	printf '%s\n' 'WebAssembly build is not reproducible' >&2
	exit 1
fi

go test -count=1 -timeout 90s ./cmd/nornrune-wasm
sha256sum "$tmp/first.wasm"
