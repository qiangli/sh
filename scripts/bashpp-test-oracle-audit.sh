#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

is_family() {
	case "$1" in
		*/concurr*_test.go|*/carrier*_test.go|*/lifecycle*_test.go|*/racedetector*_test.go|*/race_*_test.go|*signal*_test.go|*background*_test.go|*fifo*_test.go|*pipe*_test.go|*cancel*_test.go|*async*_test.go|*/tp714*_test.go|*/tp429*_test.go|*/tp65*_test.go) return 0 ;;
	esac
	grep -q 'bashpp-racegate:audit' "$1"
}

# Conservative source audit, not a proof of Go's happens-before relation.
# Reviewed declarations can opt out on their line with safe-private (never
# shared) or safe-synchronized (lock, atomic, or channel protected).
audit_file() {
	local file=$1 line number=0 fail=0 kind
	is_family "$file" || return 0
	if ! grep -qE 'go[[:space:]]+func|t\.Parallel|JobCarrier|[Cc]oncurr|[Ss]ignal|[Bb]ackground|FIFO|[Pp]ipe' "$file"; then
		return 0
	fi
	while IFS= read -r line || [ -n "$line" ]; do
		number=$((number + 1))
		[[ "$line" =~ ^[[:space:]]*// ]] && continue
		case "$line" in
			*'bashpp-racegate:safe-private'*|*'bashpp-racegate:safe-synchronized'*) continue ;;
		esac
		kind=
		if [[ "$line" =~ (bytes\.Buffer|strings\.Builder) ]]; then
			kind='plain buffer/builder'
		elif [[ "$line" =~ ^var[[:space:]].*(map\[|\[\]|bool|int|uint|\*interp\.Runner) ]]; then
			kind='package-level mutable state'
		elif [[ "$line" =~ (shared|results?|errors?|seen|flags?|state|poll)[A-Za-z0-9_]*[[:space:]]*(:=|=|[[:space:]])[[:space:]]*(make\((map|\[\])|map\[|\[\]|true|false|[0-9]+|[^[:space:]]*Runner) ]]; then
			kind='possibly shared map/slice/flag/runner state'
		fi
		if [ -n "$kind" ]; then
			printf '%s:%d: bashpp oracle audit: %s requires synchronization/private annotation: %s\n' "$file" "$number" "$kind" "$line" >&2
			fail=1
		fi
	done <"$file"
	return "$fail"
}

self_test() {
	local fixtures=scripts/testdata/bashpp-oracle-audit fail=0 file
	for file in "$fixtures"/unsafe/*.go; do
		if audit_file "$file" >/dev/null 2>&1; then
			echo "bashpp oracle audit self-test: unsafe fixture passed: $file" >&2
			fail=1
		fi
	done
	for file in "$fixtures"/safe/*.go; do
		if ! audit_file "$file"; then
			echo "bashpp oracle audit self-test: safe fixture failed: $file" >&2
			fail=1
		fi
	done
	if ((fail)); then return 1; fi
	echo "bashpp oracle audit self-test: PASS"
}

if [ "${1:-}" = --self-test ]; then self_test; exit; fi
if (($#)); then
	files=("$@")
else
	files=()
	while IFS= read -r file; do files+=("$file"); done < <(git ls-files '*_test.go' | sort)
fi
if ((${#files[@]} == 0)); then
	echo "bashpp oracle audit: no test files found" >&2
	exit 1
fi
fail=0
for file in "${files[@]}"; do audit_file "$file" || fail=1; done
if ((fail)); then
	echo "bashpp oracle audit: use a synchronized oracle, immutable snapshot, or a reviewed safe annotation" >&2
	exit 1
fi
echo "bashpp oracle audit: PASS (${#files[@]} files considered)"
