#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
command -v pgrep >/dev/null 2>&1 || {
	echo "signal cleanup self-test requires pgrep" >&2
	exit 1
}

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/bashpp-race-signal.XXXXXX")
gate_pid=
cleanup() {
	if [ -n "$gate_pid" ]; then
		kill -TERM "$gate_pid" 2>/dev/null || true
		wait "$gate_pid" 2>/dev/null || true
	fi
	rm -rf "$tmpdir"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

descendants() {
	local parent=$1 child
	for child in $(pgrep -P "$parent" 2>/dev/null || true); do
		printf '%s\n' "$child"
		descendants "$child"
	done
}

BASHPP_RACE_EVIDENCE="$tmpdir/evidence" GOMEMLIMIT=2GiB \
	/bin/bash scripts/bashpp-race-gate.sh --discovery-only >"$tmpdir/output" 2>&1 &
gate_pid=$!

tree=
for _ in 1 2 3 4 5 6 7 8 9 10; do
	tree=$(descendants "$gate_pid")
	count=$(printf '%s\n' "$tree" | grep -Ec '^[0-9]+$' || true)
	[ "$count" -ge 2 ] && break
	sleep 0.1
done
if [ "${count:-0}" -lt 2 ]; then
	echo "signal cleanup self-test: gate produced fewer than two observable descendants" >&2
	exit 1
fi

kill -TERM "$gate_pid"
set +e
wait "$gate_pid"
gate_code=$?
set -e
gate_pid=
if [ "$gate_code" -ne 143 ]; then
	echo "signal cleanup self-test: TERM exit=$gate_code, want 143" >&2
	exit 1
fi

for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
	live=
	for pid in $tree; do
		if kill -0 "$pid" 2>/dev/null; then live="$live $pid"; fi
	done
	[ -z "$live" ] && break
	sleep 0.1
done
if [ -n "$live" ]; then
	echo "signal cleanup self-test: descendants survived TERM:$live" >&2
	exit 1
fi
echo "signal cleanup self-test: PASS"
