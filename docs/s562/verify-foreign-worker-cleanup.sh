#!/bin/sh
# Manager-only Linux/Windows verification for Story 3082. This intentionally
# runs only the four static lowering checks and the one immutable foreign-smoke
# case; it does not run a foreign corpus or alter its count floor.
set -eu

phase=${1:?red or green required}
evidence=${2:?new absolute S562 evidence directory required}
case "$phase" in red|green) ;; *) exit 2 ;; esac
case "$evidence" in /*) ;; *) echo 'absolute evidence directory required' >&2; exit 2 ;; esac
: "${GO:?authenticated Go executable required}"
: "${BASHY_BIN:?candidate bashy executable required}"
: "${FENCE_SH_MODULE:?absolute matching sh module required}"
: "${FENCE_TOOLCHAIN_DIR:?retained managed fence toolchain directory required}"
: "${HARNESS_ROOT:?absolute harness checkout required}"

test ! -e "$evidence"
mkdir "$evidence"
scratch="$evidence/s562-scratch"
mkdir -p "$scratch/tmp"
printf 'monotonic_s\tpid\tppid\tpgid\tsid\tstate\tcommand\n' >"$evidence/process-states.tsv"

{
	printf 'phase=%s\n' "$phase"
	printf 'sh=%s\n' "$(git -C "$FENCE_SH_MODULE" rev-parse HEAD)"
	printf 'harness=%s\n' "$(git -C "$HARNESS_ROOT" rev-parse HEAD)"
	printf 'bashy_blob=%s\n' "$(git -C "$FENCE_SH_MODULE" hash-object "$BASHY_BIN")"
	printf 'go=%s\n' "$($GO version)"
	printf 'scratch=%s\n' "$scratch"
	for path in cases.tsv inventory.tsv counts.tsv chapter/typescript.bsh chapter/typescript.expected; do
		printf 'fixture_%s=%s\n' "$(printf '%s' "$path" | tr '/.' '__')" \
			"$(git -C "$HARNESS_ROOT" hash-object "tools/fence-corpus/testdata/foreign-smoke/$path")"
	done
} >"$evidence/identities"

static_rc=0
(cd "$FENCE_SH_MODULE" && "$GO" test ./lower \
	-run '^(TestProgramMain|TestFoundationMain)(ClosesForeignWorkersBeforeExit|WithoutForeignWorkersIsUnchanged)$' \
	-count=1) >"$evidence/static.stdout" 2>"$evidence/static.stderr" || static_rc=$?
printf '%s\n' "$static_rc" >"$evidence/static.exit-status"

fixture="$HARNESS_ROOT/tools/fence-corpus/testdata/foreign-smoke"
(cd "$HARNESS_ROOT" && sh tools/fence-corpus/validate-inventory.sh "$fixture") \
	>"$evidence/inventory.stdout" 2>"$evidence/inventory.stderr"

# Keep the harness and its containment policy untouched. An external observer
# copies the generated artifact and lineage while the one case is live, and on
# Linux samples descendants whose cwd is inside this fresh scratch directory.
# The sampler never signals or waits for a case process.
capture_case_evidence() {
	for main in "$scratch"/tmp/fence-case-*/build/main.go; do
		test -f "$main" || continue
		cp "$main" "$evidence/generated-main.go"
	done
	for ledger in "$scratch"/tmp/fence-case-*/log/*.lineage.jsonl; do
		test -f "$ledger" || continue
		cp "$ledger" "$evidence/$(basename "$ledger")"
	done
	test -d /proc/1 || return 0
	for proc in /proc/[0-9]*; do
		cwd=$(readlink "$proc/cwd" 2>/dev/null) || continue
		case "$cwd" in "$scratch"/*) ;; *) continue ;; esac
		stat_line=$(cat "$proc/stat" 2>/dev/null) || continue
		fields=${stat_line##*) }
		set -- $fields
		state=$1 ppid=$2 pgid=$3 sid=$4
		pid=${proc##*/}
		read -r monotonic _ </proc/uptime || monotonic=unknown
		command=$(tr '\000' ' ' <"$proc/cmdline" 2>/dev/null || true)
		printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
			"$monotonic" "$pid" "$ppid" "$pgid" "$sid" "$state" "$command" \
			>>"$evidence/process-states.tsv"
	done
}

smoke_rc=0
(cd "$HARNESS_ROOT" && TMPDIR="$scratch/tmp" sh tools/fence-corpus/check.sh \
	--corpus "$fixture" --mode compiled --case typescript-compiled) \
	>"$evidence/foreign-smoke.stdout" 2>"$evidence/foreign-smoke.stderr" &
smoke_pid=$!
(
	while kill -0 "$smoke_pid" 2>/dev/null; do
		capture_case_evidence
		sleep 0.02
	done
	capture_case_evidence
) &
collector_pid=$!
wait "$smoke_pid" || smoke_rc=$?
wait "$collector_pid" || true
printf '%s\n' "$smoke_rc" >"$evidence/foreign-smoke.exit-status"

cat "$evidence/static.stdout"
cat "$evidence/static.stderr" >&2
cat "$evidence/foreign-smoke.stdout"
cat "$evidence/foreign-smoke.stderr" >&2

case "$phase" in
	red)
		test "$static_rc" -ne 0
		grep -q 'generated foundation main omitted "func cleanup_mainEntry' "$evidence/static.stdout"
		test "$smoke_rc" -ne 0
		grep -q 'process_leak' "$evidence/foreign-smoke.stdout"
		;;
	green)
		test "$static_rc" -eq 0
		grep -q '^ok[[:space:]]' "$evidence/static.stdout"
		grep -q 'func __bpp[0-9]*_mainEntry()' "$evidence/generated-main.go"
		grep -q '_foreign[0-9]*.Close()' "$evidence/generated-main.go"
		grep -q '__bpp[0-9]*_mainEntry()' "$evidence/generated-main.go"
		grep -q '__bpp[0-9]*_rt.Exit()' "$evidence/generated-main.go"
		test "$smoke_rc" -eq 0
		grep -qx 'PASS foreign-smoke typescript-compiled compiled' "$evidence/foreign-smoke.stdout"
		grep -qx 'Summary: 1 cases, 1 PASS, 0 failures' "$evidence/foreign-smoke.stdout"
		! grep -q 'process_leak' "$evidence/foreign-smoke.stdout" "$evidence/foreign-smoke.stderr"
		;;
esac
