#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

exec timeout 240s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 \
  -test=false ./frontend/... ./internal/... ./cmd/... ./policies/...
