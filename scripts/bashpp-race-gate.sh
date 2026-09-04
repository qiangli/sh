#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
case "${1:-}" in
	"") discovery_only=0 ;;
	--discovery-only) discovery_only=1 ;;
	*) echo "usage: $0 [--discovery-only]" >&2; exit 2 ;;
esac

evidence=${BASHPP_RACE_EVIDENCE:-artifacts/bashpp-race-gate.txt}
mkdir -p "$(dirname "$evidence")"
: >"$evidence"
log() { printf '%s\n' "$*" | tee -a "$evidence"; }

go_bin=${GO:-go}
go_path=$(command -v "$go_bin")
go_dir=$(dirname "$go_path")
export PATH="/bin:/usr/bin:$go_dir:$PATH"
# This bounds the Go heap, not whole-process-tree RSS. CI and the sprint
# manager apply an external process-tree ceiling as well.
export GOMEMLIMIT=${GOMEMLIMIT:-2GiB}

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/bashpp-race-gate.XXXXXX")
cleanup() { rm -rf "$tmpdir"; }
trap cleanup EXIT HUP INT TERM

# Indexed arrays and a redirected while loop work in Apple's Bash 3.2.
packages=()
"$go_bin" list ./... | sort >"$tmpdir/packages"
while IFS= read -r package; do
	[ -n "$package" ] && packages+=("$package")
done <"$tmpdir/packages"
if ((${#packages[@]} == 0)); then
	log "ERROR: go list ./... returned zero packages"
	exit 1
fi

test_count=0
for pkg in "${packages[@]}"; do
	count=$("$go_bin" test -timeout=2m -list '^(Test|Example|Fuzz)' "$pkg" |
		grep -Ec '^(Test|Example|Fuzz)' || true)
	test_count=$((test_count + count))
done
if ((test_count == 0)); then
	log "ERROR: test discovery found zero test/example/fuzz targets"
	exit 1
fi

focused_re='Test(Concurrency|JobCarrier|Signal|.*Cancel|.*Background|.*FIFO|.*Pipe)'
focused_count=$("$go_bin" test -timeout=2m -list "$focused_re" ./interp |
	grep -Ec '^Test' || true)
if ((focused_count == 0)); then
	log "ERROR: focused regex discovered zero tests: $focused_re"
	exit 1
fi

log "Bash++ race/lifecycle gate"
log "go_version: $("$go_bin" version)"
log "goos: $("$go_bin" env GOOS)"
log "goarch: $("$go_bin" env GOARCH)"
log "GOMEMLIMIT: $GOMEMLIMIT (Go heap ceiling; not a hard process-tree RSS limit)"
log "package_count: ${#packages[@]}"
log "test_count: $test_count"
log "focused_test_count: $focused_count"
log "real_bash_compatibility_corpus: TestRunnerRunConfirm against Bash 5.3 (separate confirm task)"
log "packages:"
printf '  %s\n' "${packages[@]}" | tee -a "$evidence"

log ""
log "oracle_audit: scripts/bashpp-test-oracle-audit.sh"
./scripts/bashpp-test-oracle-audit.sh --self-test 2>&1 | tee -a "$evidence"
./scripts/bashpp-test-oracle-audit.sh 2>&1 | tee -a "$evidence"
if ((discovery_only)); then
	log ""
	log "discovery_only: PASS (race tests were not launched)"
	exit 0
fi

terminate_tree() {
	local parent=$1 child
	if command -v pgrep >/dev/null 2>&1; then
		for child in $(pgrep -P "$parent" 2>/dev/null || true); do
			terminate_tree "$child"
		done
	fi
	kill -TERM "$parent" 2>/dev/null || true
}

# GNU time uses -v and Darwin time uses -l; both report peak RSS. The portable
# wall-clock watchdog recursively terminates descendants when pgrep exists.
run_bounded() {
	local seconds=$1 label=$2
	shift 2
	local output=$tmpdir/output marker=$tmpdir/timeout pid watcher status=0
	rm -f "$output" "$marker"
	log "command: $label (outer_timeout=${seconds}s)"
	if [ "$(uname -s)" = Darwin ]; then
		/usr/bin/time -l "$@" >"$output" 2>&1 &
	elif /usr/bin/time -v true >/dev/null 2>&1; then
		/usr/bin/time -v "$@" >"$output" 2>&1 &
	else
		"$@" >"$output" 2>&1 &
	fi
	pid=$!
	(
		sleep "$seconds"
		if kill -0 "$pid" 2>/dev/null; then
			printf 'timeout\n' >"$marker"
			terminate_tree "$pid"
		fi
	) &
	watcher=$!
	set +e
	wait "$pid"
	status=$?
	set -e
	kill "$watcher" 2>/dev/null || true
	wait "$watcher" 2>/dev/null || true
	tee -a "$evidence" <"$output"
	if [ -f "$marker" ]; then
		log "ERROR: $label exceeded ${seconds}s"
		return 124
	fi
	return "$status"
}

for procs in 1 2 4; do
	log ""
	run_bounded 210 \
		"GOMAXPROCS=$procs go test -race -timeout=3m -count=3 -run '$focused_re' ./interp" \
		env GOMAXPROCS="$procs" "$go_bin" test -race -timeout=3m -count=3 -run "$focused_re" ./interp
done

log ""
run_bounded 750 \
	"go test -race -timeout=12m ./... -skip 'TestRunnerRunConfirm|TestParseConfirm'" \
	"$go_bin" test -race -timeout=12m ./... -skip 'TestRunnerRunConfirm|TestParseConfirm'
log ""
log "gate: PASS"
