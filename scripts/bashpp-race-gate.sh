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

stop_watcher() {
	local pid=${1:-} i state
	[ -n "$pid" ] || return 0
	kill -TERM "$pid" 2>/dev/null || true
	for i in 1 2 3 4 5 6 7 8 9 10; do
		state=$(ps -o stat= -p "$pid" 2>/dev/null || true)
		[ -z "$state" ] && break
		case "$state" in Z*) break ;; esac
		sleep 0.1
	done
	kill -KILL "$pid" 2>/dev/null || true
	wait "$pid" 2>/dev/null || true
}

start_watcher() {
	local seconds=$1 target=$2 marker=$3
	# The watchdog is a single Perl process: stopping it cannot orphan a sleep
	# subprocess. Its target lives in a separate session/process group.
	perl -e '
		my ($seconds, $target, $marker) = @ARGV;
		sleep $seconds;
		if (kill 0, -$target) {
			open my $fh, ">", $marker or die "open $marker: $!";
			print {$fh} "timeout\n";
			close $fh;
			kill "TERM", -$target;
			sleep 5;
			kill "KILL", -$target if kill 0, -$target;
		}
	' "$seconds" "$target" "$marker" &
}

# The outer invocation puts the entire gate under one deadline. The internal
# process traps TERM/INT/HUP and cleans up any nested command group before it
# exits, so the nested setsid groups cannot escape the global watchdog.
if ((!internal)); then
	# set -e aborts the gate at the FIRST lane that times out, so the worst
	# realistic run is every lane finishing plus one ceiling, not the sum of
	# every ceiling. This stays comfortably inside the workflow's own
	# timeout-minutes so the gate always reports before the job is killed —
	# a job-level kill produces no evidence at all.
	global_seconds=${BASHPP_RACE_GLOBAL_TIMEOUT_SECONDS:-1800}
	args=(--internal)
	((discovery_only)) && args+=(--discovery-only)
	marker=$(mktemp "${TMPDIR:-/tmp}/bashpp-race-global.XXXXXX")
	rm -f "$marker"
	global_pid=
	watcher=
	global_abort() {
		local code=$1
		trap - HUP INT TERM
		stop_watcher "$watcher"
		if [ -n "$global_pid" ]; then
			kill_group "$global_pid"
			wait "$global_pid" 2>/dev/null || true
		fi
		rm -f "$marker"
		exit "$code"
	}
	trap 'global_abort 129' HUP
	trap 'global_abort 130' INT
	trap 'global_abort 143' TERM
	perl -MPOSIX=setsid -e 'setsid() >= 0 or die "setsid: $!"; exec @ARGV or die "exec: $!"' \
		/bin/bash "$0" "${args[@]}" &
	global_pid=$!
	start_watcher "$global_seconds" "$global_pid" "$marker"
	watcher=$!
	set +e
	wait "$global_pid"
	status=$?
	set -e
	stop_watcher "$watcher"
	watcher=
	if [ -f "$marker" ]; then
		printf 'ERROR: Bash++ race/lifecycle gate exceeded global %ss deadline\n' "$global_seconds" | tee -a "$evidence"
		status=124
	fi
	rm -f "$marker"
	exit "$status"
fi

: >"$evidence"
log() { printf '%s\n' "$*" | tee -a "$evidence"; }
go_path=$(command -v "${GO:-go}")
go_dir=$(dirname "$go_path")
# Resolve the toolchain ONCE and invoke it by absolute path. The gate adds
# /bin and /usr/bin so the bounded commands always find a base userland, but
# those directories may hold a distro `go` (the GitHub ubuntu runners ship
# one); prepending them ahead of the selected toolchain silently downgraded
# the gate to that older Go and failed on the module's language floor.
go_bin=$go_path
export PATH="$go_dir:/bin:/usr/bin:$PATH"
# GOMEMLIMIT bounds the Go heap, not total process-tree RSS. CI records peak
# RSS; the sprint manager supplies the separate hard 3 GiB process-tree cap.
export GOMEMLIMIT=${GOMEMLIMIT:-2GiB}

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/bashpp-race-gate.XXXXXX")
active_pid=
active_watcher=
cleanup_active() {
	stop_watcher "$active_watcher"
	active_watcher=
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
	local marker=$tmpdir/timeout status=0
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
	start_watcher "$seconds" "$active_pid" "$marker"
	active_watcher=$!
	set +e
	wait "$active_pid"
	status=$?
	set -e
	active_pid=
	stop_watcher "$active_watcher"
	active_watcher=
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
log "real_bash_compatibility_corpus: TestRunnerRunConfirm against Bash 5.2 (separate confirm task)"

./scripts/bashpp-test-oracle-audit.sh --self-test 2>&1 | tee -a "$evidence"
./scripts/bashpp-test-oracle-audit.sh 2>&1 | tee -a "$evidence"
if ((discovery_only)); then
	log "discovery_only: PASS (race tests were not launched)"
	exit 0
fi

# Compile the race build BEFORE the timed lanes. A lane's bound asks "did
# this hang?", and -race compilation of ./interp is minutes of honest work
# that answers nothing about that question; counting it made the bound a
# measure of the runner's build speed. Warmed here, the lane timers below
# measure EXECUTION.
run_bounded 300 "race build" "$tmpdir/racebuild" 0 \
	"$go_bin" test -race -run '^$' ./interp

# Bounds are set from measurement with real headroom, not from the dev box's
# wall clock. Measured here after warming: 114s/94s/94s for the focused lanes
# and 99s for the whole race suite. The previous 150s outer bound sat 1.3x
# over the slowest of those AND included the build, so CI reported TIME on a
# lane that had not hung — and a lane reported as TIME says nothing at all,
# which is the failure this gate exists to prevent. What the bound must catch
# is a lifecycle leak that never terminates; that is unbounded, so a generous
# ceiling catches it exactly as well as a tight one and stops manufacturing
# false timeouts on slower hardware.
for procs in 1 2 4; do
	run_bounded 420 \
		"GOMAXPROCS=$procs go test -race -timeout=6m -count=3 -run '$focused_re' ./interp" \
		"$tmpdir/focused.$procs" 1 env GOMAXPROCS="$procs" \
		"$go_bin" test -race -timeout=6m -count=3 -run "$focused_re" ./interp
done
run_bounded 600 \
	"go test -race -timeout=8m ./... -skip 'TestRunnerRunConfirm|TestParseConfirm'" \
	"$tmpdir/all" 1 "$go_bin" test -race -timeout=8m ./... \
	-skip 'TestRunnerRunConfirm|TestParseConfirm'
log "gate: PASS"
