#!/bin/sh
set -eu

usage() {
	printf 'usage: %s BASELINE_BINARY CURRENT_BINARY BENCHMARK_REGEX [ROUNDS>=6] [BENCHTIME]\n' "$0" >&2
}

fail() {
	printf 'bench-compare: %s\n' "$1" >&2
	exit 2
}

require_command() {
	case "$1" in
	*/*) [ -x "$1" ] || fail "command is not executable: $1" ;;
	*) command -v "$1" >/dev/null 2>&1 || fail "command is unavailable: $1" ;;
	esac
}

absolute_executable() {
	directory=$(dirname -- "$1")
	filename=$(basename -- "$1")
	directory=$(CDPATH= cd -- "$directory" 2>/dev/null && pwd) || return 1
	path="$directory/$filename"
	[ -f "$path" ] && [ -x "$path" ] || return 1
	printf '%s\n' "$path"
}

if [ "$#" -eq 0 ]; then
	[ -n "${BENCH_BASELINE_BINARY:-}" ] || fail "BENCH_BASELINE_BINARY is required when no arguments are supplied"
	[ -n "${BENCH_CURRENT_BINARY:-}" ] || fail "BENCH_CURRENT_BINARY is required when no arguments are supplied"
	set -- "$BENCH_BASELINE_BINARY" "$BENCH_CURRENT_BINARY" \
		"${BENCH_COMPARE_REGEX:-^BenchmarkEvaluate$}" "${BENCH_COMPARE_ROUNDS:-6}" "${BENCH_COMPARE_TIME:-200ms}"
fi

if [ "$#" -lt 3 ] || [ "$#" -gt 5 ]; then
	usage
	exit 2
fi

baseline=$(absolute_executable "$1") || fail "baseline test binary is unavailable or not executable"
current=$(absolute_executable "$2") || fail "current test binary is unavailable or not executable"
benchmark=$3
rounds=${4:-6}
benchtime=${5:-200ms}
[ -n "$benchmark" ] || fail "benchmark regex must not be empty"
case "$rounds" in
''|*[!0-9]*) fail "rounds must be an integer within [6, 20]" ;;
esac
if [ "$rounds" -lt 6 ] || [ "$rounds" -gt 20 ]; then
	fail "rounds must be an integer within [6, 20]"
fi
case "$benchtime" in
''|-*|*[!0-9A-Za-z.]*) fail "benchtime contains unsupported characters" ;;
esac

benchstat_command=${BENCH_COMPARE_BENCHSTAT:-benchstat}
require_command "$benchstat_command"
require_command timeout

temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
baseline_results="$temporary/baseline.txt"
current_results="$temporary/current.txt"
: >"$baseline_results"
: >"$current_results"

run_benchmark() {
	binary=$1
	output=$2
	timeout 120s "$binary" \
		-test.run '^$' \
		-test.bench "$benchmark" \
		-test.benchtime "$benchtime" \
		-test.benchmem \
		-test.count 1 \
		-test.cpu 1 \
		-test.timeout 90s >>"$output"
}

printf 'baseline=%s\n' "$baseline"
printf 'current=%s\n' "$current"
printf 'benchmark=%s\n' "$benchmark"
printf 'rounds=%s\n' "$rounds"
printf 'benchtime=%s\n' "$benchtime"
if command -v go >/dev/null 2>&1; then
	go version
fi
printf 'host=%s\n' "$(uname -srmo)"

round=1
while [ "$round" -le "$rounds" ]; do
	if [ $((round % 2)) -eq 1 ]; then
		run_benchmark "$baseline" "$baseline_results"
		run_benchmark "$current" "$current_results"
	else
		run_benchmark "$current" "$current_results"
		run_benchmark "$baseline" "$baseline_results"
	fi
	round=$((round + 1))
done

"$benchstat_command" "$baseline_results" "$current_results"
