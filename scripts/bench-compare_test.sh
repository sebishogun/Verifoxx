#!/bin/sh
set -eu

repository=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT HUP INT TERM

order="$temporary/order"
export ORDER_LOG="$order"
for label in baseline current; do
	binary="$temporary/$label.test"
	cat >"$binary" <<EOF
#!/bin/sh
printf '%s\\n' '$label' >>'$order'
printf '%s\\n' 'BenchmarkEvaluate/tier=fake/rows=64/nodes=96/evidence=0%/match=25%/workers=1/mode=scalar-1 1 100 ns/op 0 B/op 0 allocs/op'
EOF
	chmod +x "$binary"
done

fake_benchstat="$temporary/fake-benchstat"
cat >"$fake_benchstat" <<'EOF'
#!/bin/sh
set -eu
printf 'benchstat baseline=%s current=%s\n' "$1" "$2"
EOF
chmod +x "$fake_benchstat"

output=$(BENCH_COMPARE_BENCHSTAT="$fake_benchstat" \
	"$repository/scripts/bench-compare.sh" "$temporary/baseline.test" "$temporary/current.test" \
	'^BenchmarkEvaluate$' 6 1x)

expected=$(printf '%s\n' baseline current current baseline baseline current current baseline baseline current current baseline)
actual=$(cat "$order")
if [ "$actual" != "$expected" ]; then
	printf 'execution order:\n%s\nwant:\n%s\n' "$actual" "$expected" >&2
	exit 1
fi
case "$output" in
*"baseline=$temporary/baseline.test"*"current=$temporary/current.test"*"benchmark=^BenchmarkEvaluate$"*"rounds=6"*"benchtime=1x"*"benchstat baseline="*) ;;
*)
	printf 'unexpected output:\n%s\n' "$output" >&2
	exit 1
	;;
esac

: >"$order"
environment_output=$(BENCH_COMPARE_BENCHSTAT="$fake_benchstat" \
	BENCH_BASELINE_BINARY="$temporary/baseline.test" BENCH_CURRENT_BINARY="$temporary/current.test" \
	BENCH_COMPARE_REGEX='^BenchmarkEvaluate$' BENCH_COMPARE_ROUNDS=6 BENCH_COMPARE_TIME=1x \
	"$repository/scripts/bench-compare.sh")
if [ "$(cat "$order")" != "$expected" ] || [ -z "$environment_output" ]; then
	printf '%s\n' 'environment-driven comparison failed' >&2
	exit 1
fi

if BENCH_COMPARE_BENCHSTAT="$fake_benchstat" \
	"$repository/scripts/bench-compare.sh" "$temporary/baseline.test" "$temporary/current.test" \
	'^BenchmarkEvaluate$' 0 1x >/dev/null 2>&1; then
	printf '%s\n' 'zero rounds unexpectedly succeeded' >&2
	exit 1
fi
if BENCH_COMPARE_BENCHSTAT="$fake_benchstat" \
	"$repository/scripts/bench-compare.sh" "$temporary/missing.test" "$temporary/current.test" \
	'^BenchmarkEvaluate$' 6 1x >/dev/null 2>&1; then
	printf '%s\n' 'missing baseline unexpectedly succeeded' >&2
	exit 1
fi

printf '%s\n' 'bench-compare contract: PASS'
