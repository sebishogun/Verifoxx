#!/bin/sh
set -eu

go test -count=1 -timeout 210s ./internal/target/wasm -run '^TestConformanceNodeMatchesNativeResultFrame$'
