#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
	printf '%s\n' 'usage: scripts/build-wasm.sh OUTPUT.wasm' >&2
	exit 2
fi

CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm \
	go build -trimpath -buildvcs=false -buildmode=c-shared -o "$1" ./cmd/nornrune-wasm
