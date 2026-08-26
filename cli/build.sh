#!/bin/sh
set -eu

script_directory=$(CDPATH= cd -P "$(dirname "$0")" && pwd)
repository=$(CDPATH= cd -P "$script_directory/.." && pwd)
build_timeout=${DEVX_BUILD_TIMEOUT:-120s}
host_only=0

while [ "$#" -gt 0 ]; do
	case $1 in
		--host-only) host_only=1 ;;
		--help|-h)
			printf '%s\n' 'Usage: ./cli/build.sh [--host-only]'
			exit 0
			;;
		*)
			printf 'build.sh: unknown argument: %s\n' "$1" >&2
			exit 2
			;;
	esac
	shift
done

if ! command -v timeout >/dev/null 2>&1; then
	printf '%s\n' 'build.sh: timeout is required' >&2
	exit 1
fi
if ! command -v go >/dev/null 2>&1; then
	printf '%s\n' 'build.sh: Go is required' >&2
	exit 1
fi

mkdir -p "$repository/bin"
cd "$repository"

build_target() {
	goos=$1
	goarch=$2
	suffix=
	if [ "$goos" = windows ]; then
		suffix=.exe
	fi
	printf 'building %s/%s\n' "$goos" "$goarch"
	timeout "$build_timeout" env CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -trimpath -ldflags="-s -w" -o "bin/devx-$goos-$goarch$suffix" ./cmd/devx
	timeout "$build_timeout" env CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -trimpath -ldflags="-s -w" -o "bin/nornrune-$goos-$goarch$suffix" ./cmd/nornrune
}

if [ "$host_only" -eq 1 ]; then
	build_target "$(go env GOOS)" "$(go env GOARCH)"
	exit 0
fi

for target in \
	linux/amd64 linux/arm64 \
	darwin/amd64 darwin/arm64 \
	windows/amd64 windows/arm64
do
	build_target "${target%/*}" "${target#*/}"
done
