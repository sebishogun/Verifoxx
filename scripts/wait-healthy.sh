#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
	printf '%s\n' 'Usage: wait-healthy.sh URL TIMEOUT_SECONDS' >&2
	exit 2
fi

url=$1
timeout_seconds=$2
case $timeout_seconds in
	''|*[!0-9]*)
		printf '%s\n' 'wait-healthy.sh: timeout must be an integer from 1 to 3600' >&2
		exit 2
		;;
esac
if [ "$timeout_seconds" -lt 1 ] || [ "$timeout_seconds" -gt 3600 ]; then
	printf '%s\n' 'wait-healthy.sh: timeout must be an integer from 1 to 3600' >&2
	exit 2
fi
if ! command -v curl >/dev/null 2>&1; then
	printf '%s\n' 'wait-healthy.sh: curl is required' >&2
	exit 1
fi

deadline=$(( $(date +%s) + timeout_seconds ))
while ! curl --connect-timeout 1 --max-time 1 --fail --silent --output /dev/null "$url"; do
	if [ "$(date +%s)" -ge "$deadline" ]; then
		printf 'wait-healthy.sh: timed out waiting for %s\n' "$url" >&2
		exit 1
	fi
	sleep 1
done
