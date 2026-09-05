#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

oracle_manifest=scripts/bashpp-oracle-files.txt

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
	local file=$1 forced=${2:-0} line number=0 fail=0 kind
	if ((forced == 0)) && ! is_family "$file"; then
		audit_skipped=$((audit_skipped + 1))
		return 0
	fi
	if ((forced == 0)) && ! grep -qE 'go[[:space:]]+func|t\.Parallel|JobCarrier|[Cc]oncurr|[Ss]ignal|[Bb]ackground|FIFO|[Pp]ipe' "$file"; then
		audit_skipped=$((audit_skipped + 1))
		return 0
	fi
	audit_scanned=$((audit_scanned + 1))
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

audit_scanned=0
audit_skipped=0

finish_audit() {
	local total=$1 fail=$2
	if ((audit_scanned == 0)); then
		echo "bashpp oracle audit: FAIL (scanned=0 skipped=$audit_skipped total=$total); production audit scanned no files" >&2
		return 1
	fi
	if ((fail)); then
		echo "bashpp oracle audit: FAIL (scanned=$audit_scanned skipped=$audit_skipped total=$total); use a synchronized oracle, immutable snapshot, or a reviewed safe annotation" >&2
		return 1
	fi
	echo "bashpp oracle audit: PASS (scanned=$audit_scanned skipped=$audit_skipped total=$total)"
}

self_test() {
	local fixtures=scripts/testdata/bashpp-oracle-audit fail=0 file before saved_scanned saved_skipped
	for file in "$fixtures"/unsafe/*.go; do
		before=$audit_scanned
		force=0
		case "$file" in *bashpp_unmarked_oracle_test.go) force=1 ;; esac
		if audit_file "$file" "$force" >/dev/null 2>&1; then
			echo "bashpp oracle audit self-test: unsafe fixture passed: $file" >&2
			fail=1
		fi
		if [[ "$file" == *bashpp_unmarked_oracle_test.go ]] && ((audit_scanned == before)); then
			echo "bashpp oracle audit self-test: forced unmarked Bash++ file was skipped: $file" >&2
			fail=1
		fi
	done
	for file in "$fixtures"/safe/*.go; do
		if ! audit_file "$file"; then
			echo "bashpp oracle audit self-test: safe fixture failed: $file" >&2
			fail=1
		fi
	done
	# A production selector bug must not turn an empty audit into PASS.
	saved_scanned=$audit_scanned
	saved_skipped=$audit_skipped
	audit_scanned=0
	audit_skipped=1
	if finish_audit 1 0 >/dev/null 2>&1; then
		echo "bashpp oracle audit self-test: empty production scan passed" >&2
		fail=1
	fi
	audit_scanned=$saved_scanned
	audit_skipped=$saved_skipped
	if ((fail)); then return 1; fi
	echo "bashpp oracle audit self-test: PASS"
}

if [ "${1:-}" = --self-test ]; then self_test; exit; fi
if (($#)); then
	files=("$@")
else
	[ -f "$oracle_manifest" ] || { echo "bashpp oracle audit: missing $oracle_manifest" >&2; exit 1; }
	manifest_files=()
	while IFS= read -r file || [ -n "$file" ]; do
		case "$file" in ''|'#'*) continue ;; esac
		[ -f "$file" ] || { echo "bashpp oracle audit: manifested file missing: $file" >&2; exit 1; }
		manifest_files+=("$file")
	done <"$oracle_manifest"
	((${#manifest_files[@]} > 0)) || { echo "bashpp oracle audit: empty $oracle_manifest" >&2; exit 1; }
	files=()
	while IFS= read -r file; do files+=("$file"); done < <(
		git ls-files '*_test.go' |
			grep -v '^scripts/testdata/bashpp-oracle-audit/' |
			sort
	)
	# Existence alone is insufficient: an untracked file could make a forced
	# oracle appear covered locally while CI never receives or enumerates it.
	for file in "${manifest_files[@]}"; do
		enumerated=0
		for candidate in "${files[@]}"; do
			if [ "$candidate" = "$file" ]; then enumerated=1; break; fi
		done
		if ((enumerated == 0)); then
			echo "bashpp oracle audit: manifested file is not tracked/enumerated: $file" >&2
			exit 1
		fi
	done
fi
if ((${#files[@]} == 0)); then
	echo "bashpp oracle audit: no test files found" >&2
	exit 1
fi
fail=0
for file in "${files[@]}"; do
	forced=0
	if [ -f "$oracle_manifest" ] && grep -Fqx -- "$file" "$oracle_manifest"; then forced=1; fi
	audit_file "$file" "$forced" || fail=1
done
finish_audit "${#files[@]}" "$fail"
