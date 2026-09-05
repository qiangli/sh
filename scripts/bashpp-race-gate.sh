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
	# Every operation below has its own diagnostic bound. With the fail-closed
	# 20-package ceiling, their aggregate maximum is 3600s:
	#
	#   list 60 + package discovery 20*60 + focused discovery 60
	#   + two oracle audits 2*60 + race build 300
	#   + focused lanes 3*420 + full race suite 600
	#
	# Keep the global watchdog beyond that sum, with one minute for process
	# startup, evidence copying, and cleanup. It must never erase the more useful
	# label from a later lane's own timeout diagnostic.
	global_seconds=${BASHPP_RACE_GLOBAL_TIMEOUT_SECONDS:-3660}
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

# The reviewed manifest is platform-aware; the gate reads a RESOLVED copy.
#
# A row may carry an optional 4th field naming the GOOS it belongs to. Rows
# without one are required everywhere; a row with one is required only on that
# platform and dropped elsewhere, so a build-tagged test does not read as
# "manifested test missing" on a runner where it cannot exist.
#
# Resolving ONCE here, rather than teaching all nine parse sites a second
# arity, keeps every later `NF == 3` check exactly as reviewed.
manifest_source=scripts/bashpp-race-manifest.txt
manifest=$tmpdir/manifest.resolved
awk -F '|' -v goos="$("$go_bin" env GOOS)" '
	/^#/ { next }
	NF == 3 { print; next }
	NF == 4 && $4 == goos { print $1 "|" $2 "|" $3 }
' "$manifest_source" >"$manifest"
if [ ! -s "$manifest" ]; then
	log "ERROR: resolved manifest is empty for GOOS=$("$go_bin" env GOOS)"
	exit 1
fi
required_manifest_packages=(./syntax ./syntax/typedjson ./interp)
required_manifest_categories=(
	parser-lowering typedjson channel select range task resource exec output
	arbitration cancellation signal panic process-boundary adversarial
	capability-policy native-evaluator job-carrier fifo pipeline background
)

manifest_has_category() {
	local file=$1 wanted=$2
	awk -F '|' -v wanted="$wanted" '
		$0 !~ /^#/ && NF == 3 && $2 == wanted { found=1 }
		END { exit !found }
	' "$file"
}

manifest_has_package() {
	local file=$1 wanted=$2
	awk -F '|' -v wanted="$wanted" '
		$0 !~ /^#/ && NF == 3 && $1 == wanted { found=1 }
		END { exit !found }
	' "$file"
}

manifest_package_is_required() {
	local wanted=$1 package
	for package in "${required_manifest_packages[@]}"; do
		[ "$package" = "$wanted" ] && return 0
	done
	return 1
}

validate_manifest_shape() {
	local file=$1 package category test extra fail=0
	[ -f "$file" ] || { log "ERROR: missing Bash++ race manifest: $file"; return 1; }
	while IFS='|' read -r package category test extra || [ -n "$package$category$test$extra" ]; do
		case "$package" in ''|'#'*) continue ;; esac
		if [ -n "$extra" ] || [[ ! "$package" =~ ^\./[A-Za-z0-9_./-]+$ ]] ||
			[[ ! "$category" =~ ^[a-z][a-z0-9-]*$ ]] ||
			[[ ! "$test" =~ ^Test[A-Za-z0-9_]+$ ]]; then
			log "ERROR: malformed Bash++ race manifest row: $package|$category|$test${extra:+|$extra}"
			fail=1
		elif ! manifest_package_is_required "$package"; then
			log "ERROR: unreviewed package in Bash++ race manifest: $package"
			fail=1
		fi
	done <"$file"
	if [ -n "$(awk -F '|' '$0 !~ /^#/ && NF == 3 { print }' "$file" | sort | uniq -d)" ]; then
		log "ERROR: duplicate Bash++ race manifest row"
		fail=1
	fi
	if [ -n "$(awk -F '|' '$0 !~ /^#/ && NF == 3 { print $1 "|" $3 }' "$file" | sort | uniq -d)" ]; then
		log "ERROR: one manifested test is assigned to multiple categories"
		fail=1
	fi
	for package in "${required_manifest_packages[@]}"; do
		manifest_has_package "$file" "$package" || { log "ERROR: manifest has no exact members for package $package"; fail=1; }
	done
	for category in "${required_manifest_categories[@]}"; do
		manifest_has_category "$file" "$category" || { log "ERROR: manifest has no exact members for category $category"; fail=1; }
	done
	((fail == 0))
}

# A selector-looking test name is deliberately assigned only to "task". The
# exact category column must not infer select/channel/resource/native coverage
# from words embedded in that unrelated name.
misleading_manifest=$tmpdir/misleading.manifest
printf '%s\n' './interp|task|TestSelectChannelResourceNativeEvaluatorLookalike' >"$misleading_manifest"
for category in select channel resource native-evaluator; do
	if manifest_has_category "$misleading_manifest" "$category"; then
		log "ERROR: manifest self-test inferred $category from an unrelated test name"
		exit 1
	fi
done
log "exact manifest category self-test: PASS"
validate_manifest_shape "$manifest"
for category in "${required_manifest_categories[@]}"; do
	count=$(awk -F '|' -v wanted="$category" '$0 !~ /^#/ && NF == 3 && $2 == wanted { count++ } END { print count+0 }' "$manifest")
	log "manifest_category_${category}_row_count: $count"
done
all_manifest_names=$tmpdir/manifest.all.names
awk -F '|' '$0 !~ /^#/ && NF == 3 { print $3 }' "$manifest" | sort -u >"$all_manifest_names"
focused_count=$(awk -F '|' '$0 !~ /^#/ && NF == 3 { count++ } END { print count+0 }' "$manifest")
((focused_count > 0)) || { log "ERROR: Bash++ race manifest has zero members"; exit 1; }
focused_re=$(awk 'BEGIN { printf "^(" } { if (NR > 1) printf "|"; printf "%s", $0 } END { print ")$" }' "$all_manifest_names")
legacy_re='^Test(Concurrency|JobCarrier|Signal|.*Cancel|.*Background|.*FIFO|.*Pipe)'

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
# The outer watchdog is derived from this ceiling. Fail closed if the module
# grows past it rather than silently invalidating the aggregate deadline.
max_packages=20
if ((${#packages[@]} > max_packages)); then
	log "ERROR: package count ${#packages[@]} exceeds deadline budget ceiling $max_packages"
	exit 1
fi

test_count=0
manifest_index=0
legacy_focused_count=0
module_path=$(awk '$1 == "module" { print $2; exit }' go.mod)
[ -n "$module_path" ] || { log "ERROR: go.mod has no module path"; exit 1; }
index=0
for pkg in "${packages[@]}"; do
	index=$((index + 1))
	output=$tmpdir/list.$index
	case "$pkg" in
		"$module_path") manifest_pkg=. ;;
		"$module_path"/*) manifest_pkg=./${pkg#"$module_path"/} ;;
		*) manifest_pkg=$pkg ;;
	esac
	run_bounded 60 "test discovery: $pkg" "$output" 0 \
		"$go_bin" test -timeout=55s -list '^(Test|Example|Fuzz)' "$pkg"
	count=$(grep -Ec '^(Test|Example|Fuzz)' "$output" || true)
	test_count=$((test_count + count))
	if manifest_has_package "$manifest" "$manifest_pkg"; then
		manifest_index=$((manifest_index + 1))
		names=$tmpdir/manifest.$manifest_index.names
		focused_output=$tmpdir/manifest.$manifest_index.focused
		awk -F '|' -v package="$manifest_pkg" '$0 !~ /^#/ && NF == 3 && $1 == package { print $3 }' "$manifest" | sort -u >"$names"
		count=$(wc -l <"$names" | tr -d ' ')
		if ((count == 0)); then
			log "ERROR: manifest produced zero focused tests for $pkg"
			exit 1
		fi
		# Derive exact selection from the one bounded full-discovery result.
		# Comparing BOTH directions catches missing/excluded members and a
		# same-named test accidentally selected in the wrong package.
		grep -E "$focused_re" "$output" | sort -u >"$focused_output" || true
		while IFS= read -r test; do
			if ! grep -Fqx -- "$test" "$focused_output"; then
				log "ERROR: manifested test missing or excluded from focused selection: $pkg $test"
				exit 1
			fi
		done <"$names"
		while IFS= read -r test; do
			if ! grep -Fqx -- "$test" "$names"; then
				log "ERROR: global focused selector admitted unmanifested test in $pkg: $test"
				exit 1
			fi
		done <"$focused_output"
		selected=$(grep -Ec '^Test' "$focused_output" || true)
		if ((selected != count)); then
			log "ERROR: focused selection for $pkg returned $selected tests; manifest has $count exact members"
			exit 1
		fi
		log "manifest_${manifest_index}_package: $pkg"
		log "manifest_${manifest_index}_focused_test_count: $count"
		if [ "$manifest_pkg" = ./interp ]; then
			legacy_output=$tmpdir/legacy.focused
			grep -E "$legacy_re" "$output" | sort -u >"$legacy_output" || true
			while IFS= read -r test; do
				if ! grep -Fqx -- "$test" "$names"; then
					log "ERROR: prior lifecycle selector member is absent from exact manifest: $test"
					exit 1
				fi
			done <"$legacy_output"
			legacy_focused_count=$(wc -l <"$legacy_output" | tr -d ' ')
		fi
	fi
done
if ((test_count == 0)); then
	log "ERROR: test discovery found zero test/example/fuzz targets"
	exit 1
fi
if ((manifest_index != ${#required_manifest_packages[@]})); then
	log "ERROR: discovered $manifest_index of ${#required_manifest_packages[@]} required manifest packages"
	exit 1
fi
if ((legacy_focused_count < 80)); then
	log "ERROR: prior lifecycle selector retained $legacy_focused_count tests; expected at least 80"
	exit 1
fi

log "Bash++ race/lifecycle gate"
log "global_deadline_seconds: ${BASHPP_RACE_GLOBAL_TIMEOUT_SECONDS:-3660}"
log "go_version: $("$go_bin" version)"
log "goos: $("$go_bin" env GOOS)"
log "goarch: $("$go_bin" env GOARCH)"
log "GOMEMLIMIT: $GOMEMLIMIT (Go heap ceiling; not a hard process-tree RSS limit)"
log "package_count: ${#packages[@]}"
log "test_count: $test_count"
log "focused_test_count: $focused_count"
log "prior_lifecycle_focused_test_count: $legacy_focused_count"
log "manifest: $manifest"
log "real_bash_compatibility_corpus: TestRunnerRunConfirm against Bash 5.2 (separate confirm task)"

run_bounded 60 "oracle audit self-test" "$tmpdir/oracle-self" 0 \
	./scripts/bashpp-test-oracle-audit.sh --self-test
run_bounded 60 "oracle audit" "$tmpdir/oracle" 0 \
	./scripts/bashpp-test-oracle-audit.sh
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
	"$go_bin" test -race -run '^$' "${required_manifest_packages[@]}"

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
		"GOMAXPROCS=$procs go test -race -timeout=6m -count=3 -run '$focused_re' ${required_manifest_packages[*]}" \
		"$tmpdir/focused.$procs" 1 env GOMAXPROCS="$procs" \
		"$go_bin" test -race -timeout=6m -count=3 -run "$focused_re" "${required_manifest_packages[@]}"
done
run_bounded 600 \
	"go test -race -timeout=8m ./... -skip 'TestRunnerRunConfirm|TestParseConfirm'" \
	"$tmpdir/all" 1 "$go_bin" test -race -timeout=8m ./... \
	-skip 'TestRunnerRunConfirm|TestParseConfirm'
log "gate: PASS"
