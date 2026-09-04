#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

discovery_only=0
internal=0
for arg in "$@"; do
	case "$arg" in
		--discovery-only) discovery_only=1 ;;
		--internal) internal=1 ;;
		*) echo "usage: $0 [--discovery-only]" >&2; exit 2 ;;
	esac
done

evidence=${BASHPP_RACE_EVIDENCE:-artifacts/bashpp-race-gate.txt}
mkdir -p "$(dirname "$evidence")"

# POSIX::setsid ships with the system Perl on macOS and Linux. Every bounded
# command gets an isolated process group, so cleanup does not depend on pgrep.
command -v perl >/dev/null 2>&1 || {
	echo "bashpp race gate requires perl for portable process-group cleanup" >&2
	exit 1
}

kill_group() {
	local pid=$1 i
	kill -TERM -- "-$pid" 2>/dev/null || true
	for i in 1 2 3 4 5 6 7 8 9 10; do
		kill -0 -- "-$pid" 2>/dev/null || return 0
		sleep 0.5
	done
	kill -KILL -- "-$pid" 2>/dev/null || true
	for i in 1 2 3 4 5 6 7 8 9 10; do
		kill -0 -- "-$pid" 2>/dev/null || return 0
		sleep 0.1
	done
}

# The outer invocation puts the entire gate under one deadline. The internal
# process traps TERM/INT/HUP and cleans up any nested command group before it
# exits, so the nested setsid groups cannot escape the global watchdog.
if ((!internal)); then
	global_seconds=${BASHPP_RACE_GLOBAL_TIMEOUT_SECONDS:-900}
	args=(--internal)
	((discovery_only)) && args+=(--discovery-only)
	marker=$(mktemp "${TMPDIR:-/tmp}/bashpp-race-global.XXXXXX")
	rm -f "$marker"
	perl -MPOSIX=setsid -e 'setsid() >= 0 or die "setsid: $!"; exec @ARGV or die "exec: $!"' \
		/bin/bash "$0" "${args[@]}" &
	global_pid=$!
	global_abort() {
		kill_group "$global_pid"
		wait "$global_pid" 2>/dev/null || true
		rm -f "$marker"
		exit 130
	}
	trap global_abort HUP INT TERM
	(
		sleep "$global_seconds"
		if kill -0 "$global_pid" 2>/dev/null; then
			printf 'timeout\n' >"$marker"
			kill_group "$global_pid"
		fi
	) &
	watcher=$!
	set +e
	wait "$global_pid"
	status=$?
	set -e
	kill "$watcher" 2>/dev/null || true
	wait "$watcher" 2>/dev/null || true
	if [ -f "$marker" ]; then
		printf 'ERROR: Bash++ race/lifecycle gate exceeded global %ss deadline\n' "$global_seconds" | tee -a "$evidence"
		status=124
	fi
	rm -f "$marker"
	exit "$status"
fi

: >"$evidence"
log() { printf '%s\n' "$*" | tee -a "$evidence"; }
go_bin=${GO:-go}
go_path=$(command -v "$go_bin")
go_dir=$(dirname "$go_path")
export PATH="/bin:/usr/bin:$go_dir:$PATH"
# GOMEMLIMIT bounds the Go heap, not total process-tree RSS. CI records peak
# RSS; the sprint manager supplies the separate hard 3 GiB process-tree cap.
export GOMEMLIMIT=${GOMEMLIMIT:-2GiB}

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/bashpp-race-gate.XXXXXX")
active_pid=
cleanup_active() {
	if [ -n "$active_pid" ]; then
		kill_group "$active_pid"
		wait "$active_pid" 2>/dev/null || true
		active_pid=
	fi
}
cleanup() { cleanup_active; rm -rf "$tmpdir"; }
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# Run a command in its own session with TERM, a five-second grace period,
# KILL, and parent-side reap. Set measure=1 for Darwin/GNU peak-RSS evidence.
run_bounded() {
	local seconds=$1 label=$2 output=$3 measure=$4
	shift 4
	local marker=$tmpdir/timeout watcher status=0
	rm -f "$output" "$marker"
	[ -n "$label" ] && log "command: $label (outer_timeout=${seconds}s)"
	if ((measure)) && [ "$(uname -s)" = Darwin ]; then
		perl -MPOSIX=setsid -e 'setsid() >= 0 or die "setsid: $!"; exec @ARGV or die "exec: $!"' \
			/usr/bin/time -l "$@" >"$output" 2>&1 &
	elif ((measure)) && /usr/bin/time -v true >/dev/null 2>&1; then
		perl -MPOSIX=setsid -e 'setsid() >= 0 or die "setsid: $!"; exec @ARGV or die "exec: $!"' \
			/usr/bin/time -v "$@" >"$output" 2>&1 &
	else
		perl -MPOSIX=setsid -e 'setsid() >= 0 or die "setsid: $!"; exec @ARGV or die "exec: $!"' \
			"$@" >"$output" 2>&1 &
	fi
	active_pid=$!
	(
		sleep "$seconds"
		if kill -0 "$active_pid" 2>/dev/null; then
			printf 'timeout\n' >"$marker"
			kill_group "$active_pid"
		fi
	) &
	watcher=$!
	set +e
	wait "$active_pid"
	status=$?
	set -e
	active_pid=
	kill "$watcher" 2>/dev/null || true
	wait "$watcher" 2>/dev/null || true
	if [ -n "$label" ]; then tee -a "$evidence" <"$output"; fi
	if [ -f "$marker" ]; then
		log "ERROR: $label exceeded ${seconds}s"
		return 124
	fi
	return "$status"
}

# Indexed arrays and redirected while loops work with Apple's Bash 3.2.
packages=()
run_bounded 60 "go list ./..." "$tmpdir/packages.unsorted" 0 "$go_bin" list ./...
sort "$tmpdir/packages.unsorted" >"$tmpdir/packages"
while IFS= read -r package; do
	[ -n "$package" ] && packages+=("$package")
done <"$tmpdir/packages"
if ((${#packages[@]} == 0)); then
	log "ERROR: go list ./... returned zero packages"
	exit 1
fi

test_count=0
index=0
for pkg in "${packages[@]}"; do
	index=$((index + 1))
	output=$tmpdir/list.$index
	run_bounded 60 "test discovery: $pkg" "$output" 0 \
		"$go_bin" test -timeout=55s -list '^(Test|Example|Fuzz)' "$pkg"
	count=$(grep -Ec '^(Test|Example|Fuzz)' "$output" || true)
	test_count=$((test_count + count))
done
if ((test_count == 0)); then
	log "ERROR: test discovery found zero test/example/fuzz targets"
	exit 1
fi

focused_re='Test(Concurrency|JobCarrier|Signal|.*Cancel|.*Background|.*FIFO|.*Pipe)'
run_bounded 60 "focused test discovery" "$tmpdir/focused" 0 \
	"$go_bin" test -timeout=55s -list "$focused_re" ./interp
focused_count=$(grep -Ec '^Test' "$tmpdir/focused" || true)
if ((focused_count == 0)); then
	log "ERROR: focused regex discovered zero tests: $focused_re"
	exit 1
fi

log "Bash++ race/lifecycle gate"
log "global_deadline_seconds: ${BASHPP_RACE_GLOBAL_TIMEOUT_SECONDS:-900}"
log "go_version: $("$go_bin" version)"
log "goos: $("$go_bin" env GOOS)"
log "goarch: $("$go_bin" env GOARCH)"
log "GOMEMLIMIT: $GOMEMLIMIT (Go heap ceiling; not a hard process-tree RSS limit)"
log "package_count: ${#packages[@]}"
log "test_count: $test_count"
log "focused_test_count: $focused_count"
log "real_bash_compatibility_corpus: TestRunnerRunConfirm against Bash 5.3 (separate confirm task)"

./scripts/bashpp-test-oracle-audit.sh --self-test 2>&1 | tee -a "$evidence"
./scripts/bashpp-test-oracle-audit.sh 2>&1 | tee -a "$evidence"
if ((discovery_only)); then
	log "discovery_only: PASS (race tests were not launched)"
	exit 0
fi

for procs in 1 2 4; do
	run_bounded 150 \
		"GOMAXPROCS=$procs go test -race -timeout=2m -count=3 -run '$focused_re' ./interp" \
		"$tmpdir/focused.$procs" 1 env GOMAXPROCS="$procs" \
		"$go_bin" test -race -timeout=2m -count=3 -run "$focused_re" ./interp
done
run_bounded 420 \
	"go test -race -timeout=6m ./... -skip 'TestRunnerRunConfirm|TestParseConfirm'" \
	"$tmpdir/all" 1 "$go_bin" test -race -timeout=6m ./... \
	-skip 'TestRunnerRunConfirm|TestParseConfirm'
log "gate: PASS"
