#!/usr/bin/env bash
# The release test tier. Build each package's full-tag test binary once, then
# run every top-level test, example, and regular fuzz seed corpus in a fresh
# process. A panic therefore fails only its selected child; it cannot prevent
# later names from being run and recorded.
set -uo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# This is a release gate, not a developer convenience wrapper. Package
# selection and the per-process timeout are fixed so an invocation cannot
# quietly run a smaller or more permissive test tier.
for override in FULL_TEST_ROOT_PACKAGES FULL_TEST_MOREINTERP_PACKAGES FULL_TEST_SKIP_MOREINTERP TEST_FULL_TIMEOUT; do
	if [[ -v $override ]]; then
		echo "ERROR: $override is not permitted by the full release test runner" >&2
		exit 2
	fi
done
timeout=30m
workdir=${FULL_TEST_WORKDIR:-}
evidence=${FULL_TEST_EVIDENCE_DIR:-"$root/artifacts/full-test"}
keep_workdir=0

usage() { echo "usage: $0 [--keep-workdir]" >&2; }
for arg in "$@"; do
	case "$arg" in --keep-workdir) keep_workdir=1 ;; *) usage; exit 2 ;; esac
done

if [ -z "$workdir" ]; then
	workdir=$(mktemp -d "${TMPDIR:-/tmp}/sh-full-test.XXXXXX")
	remove_workdir=1
else
	mkdir -p "$workdir"
	remove_workdir=0
fi
mkdir -p "$evidence"

# Test binaries are disposable, but evidence never belongs in the temp tree.
# A distinct raw-log directory preserves failures through subsequent runs.
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
logs="$evidence/raw-logs/$run_id"
mkdir -p "$logs"
cleanup() {
	if ((remove_workdir && !keep_workdir)); then rm -rf "$workdir"; else echo "full-test workdir: $workdir"; fi
}
trap cleanup EXIT

packages_file=$evidence/packages.tsv
inventory=$evidence/inventory.tsv
results=$evidence/results.tsv
commands=$evidence/run-inventory.tsv
metadata=$evidence/metadata.tsv
: >"$packages_file"
printf 'module\tpackage\tdirectory\ttest_go_files\tx_test_go_files\n' >"$inventory"
printf 'module\tpackage\tname\tkind\tstatus\n' >"$results"
printf 'sequence\tphase\tmodule\tpackage\tname\tcwd\tcommand\tlog\tstatus\n' >"$commands"

quote_command() {
	local arg quoted='' escaped
	for arg in "$@"; do printf -v escaped '%q' "$arg"; quoted+="${quoted:+ }$escaped"; done
	printf '%s' "$quoted"
}
candidate_sha=$(git -C "$root" rev-parse HEAD 2>/dev/null || printf unknown)
candidate_dirty=clean
if ! git -C "$root" diff --quiet || ! git -C "$root" diff --cached --quiet; then candidate_dirty=dirty; fi
{
	printf 'key\tvalue\n'
	printf 'candidate_sha\t%s\n' "$candidate_sha"
	printf 'candidate_dirty\t%s\n' "$candidate_dirty"
	printf 'invocation\t%s\n' "$(quote_command "$0" "$@")"
	printf 'timeout\t%s\n' "$timeout"
	printf 'host\t%s\n' "$(hostname)"
	printf 'host_kernel\t%s\n' "$(uname -srm)"
	printf 'go_version\t%s\n' "$(go version)"
	printf 'runner_bash\t%s\n' "$BASH_VERSION"
	printf 'raw_logs\t%s\n' "$logs"
} >"$metadata"

status=0 discovered=0 executed=0 packages_enumerated=0 packages_compiled=0
packages_no_test_files=0 compile_failures=0 discovery_failures=0
top_pass=0 top_fail=0 top_skip=0 top_unrun=0 sub_pass=0 sub_fail=0 sub_skip=0 command_number=0

record_command() {
	local phase=$1 module=$2 package=$3 name=$4 cwd=$5 log=$6 result=$7
	shift 7
	command_number=$((command_number + 1))
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$command_number" "$phase" "$module" "$package" "$name" "$cwd" "$(quote_command "$@")" "$log" "$result" >>"$commands"
}

list_packages() {
	local module=$1 directory=$2 pattern=$3 list_log=$4
	if ! (cd "$directory" && go list -tags full -f '{{.ImportPath}}|{{.Dir}}|{{len .TestGoFiles}}|{{len .XTestGoFiles}}' "$pattern") >"$list_log" 2>&1; then
		cat "$list_log" >&2
		record_command enumerate "$module" - - "$directory" "$list_log" fail go list -tags full -f '{{.ImportPath}}|{{.Dir}}|{{len .TestGoFiles}}|{{len .XTestGoFiles}}' "$pattern"
		return 1
	fi
	record_command enumerate "$module" - - "$directory" "$list_log" pass go list -tags full -f '{{.ImportPath}}|{{.Dir}}|{{len .TestGoFiles}}|{{len .XTestGoFiles}}' "$pattern"
	while IFS='|' read -r package package_dir test_go_files x_test_go_files; do
		printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$module" "$directory" "$package" "$package_dir" "$test_go_files" "$x_test_go_files" >>"$packages_file"
	done <"$list_log"
}

if ! list_packages root "$root" ./... "$logs/root.enumerate.log"; then echo 'ERROR: could not enumerate root module packages' >&2; exit 1; fi
if ! list_packages moreinterp "$root/moreinterp" ./... "$logs/moreinterp.enumerate.log"; then echo 'ERROR: could not enumerate moreinterp packages' >&2; exit 1; fi

# A sequential package number avoids depending on import paths being safe file
# names and makes retained command/log paths easy to correlate with rows.
number=0
while IFS=$'\t' read -r module directory package package_dir test_go_files x_test_go_files; do
	number=$((number + 1)); packages_enumerated=$((packages_enumerated + 1))
	binary=$workdir/$number.test
	compile_log=$logs/$number.compile.log
	printf '%s\t%s\t%s\t%s\t%s\n' "$module" "$package" "$package_dir" "$test_go_files" "$x_test_go_files" >>"$inventory"
	if ! (cd "$directory" && go test -tags full -timeout="$timeout" -c -o "$binary" "$package") >"$compile_log" 2>&1; then
		cat "$compile_log" >&2
		record_command compile "$module" "$package" - "$directory" "$compile_log" fail go test -tags full -timeout="$timeout" -c -o "$binary" "$package"
		printf '%s\t%s\t-\tcompile\tfail\n' "$module" "$package" >>"$results"
		compile_failures=$((compile_failures + 1)); status=1; continue
	fi
	record_command compile "$module" "$package" - "$directory" "$compile_log" pass go test -tags full -timeout="$timeout" -c -o "$binary" "$package"
	packages_compiled=$((packages_compiled + 1))
	if ((test_go_files == 0 && x_test_go_files == 0)); then
		printf '%s\t%s\t-\tpackage\tno-test-files\n' "$module" "$package" >>"$results"
		packages_no_test_files=$((packages_no_test_files + 1)); continue
	fi
	list_log=$logs/$number.list.log
	if ! "$binary" -test.list=. >"$list_log" 2>&1; then
		cat "$list_log" >&2
		record_command discover "$module" "$package" - "$package_dir" "$list_log" fail "$binary" -test.list=.
		printf '%s\t%s\t-\tdiscovery\tfail\n' "$module" "$package" >>"$results"
		discovery_failures=$((discovery_failures + 1)); status=1; continue
	fi
	record_command discover "$module" "$package" - "$package_dir" "$list_log" pass "$binary" -test.list=.
	while IFS= read -r name; do
		case "$name" in Test*) kind=test ;; Example*) kind=example ;; Fuzz*) kind=fuzz ;; *) continue ;; esac
		discovered=$((discovered + 1))
		printf '%s\t%s\t%s\t%s\tdiscovered\n' "$module" "$package" "$name" "$kind" >>"$results"
		run_log=$logs/$number.$discovered.run.log
		# The anchored selector keeps one top-level test/example/fuzz seed corpus
		# per fresh process; -test.v emits explicit subtest outcomes.
		if (cd "$package_dir" && "$binary" -test.timeout="$timeout" -test.v -test.run="^${name}$") >"$run_log" 2>&1; then
			process_status=pass
		else
			process_status=fail
			status=1
			cat "$run_log" >&2
		fi
		record_command run "$module" "$package" "$name" "$package_dir" "$run_log" "$process_status" "$binary" -test.timeout="$timeout" -test.v -test.run="^${name}$"
		executed=$((executed + 1))
		# A skipped top-level test exits zero; retain skip rather than calling it pass.
		observed_top=$(awk -v name="$name" '$1 == "---" && ($2 == "PASS:" || $2 == "FAIL:" || $2 == "SKIP:") && $3 == name { gsub(":", "", $2); print tolower($2); exit }' "$run_log")
		if [ -n "$observed_top" ]; then
			run_status=$observed_top
		elif [ "$process_status" = pass ]; then
			# An exit status alone is not evidence that the selected top-level
			# name ran. Keep the raw log and fail the gate loudly for review.
			run_status=unrun
			status=1
			echo "ERROR: $module $package $name exited zero without a named top-level outcome" >&2
		else
			run_status=fail
		fi
		case "$run_status" in pass) top_pass=$((top_pass + 1)) ;; fail) top_fail=$((top_fail + 1)) ;; skip) top_skip=$((top_skip + 1)) ;; unrun) top_unrun=$((top_unrun + 1)) ;; esac
		printf '%s\t%s\t%s\t%s\t%s\n' "$module" "$package" "$name" "$kind" "$run_status" >>"$results"
		while IFS=$'\t' read -r sub_status subtest; do
			[ "$subtest" = "$name" ] && continue
			case "$sub_status" in pass) sub_pass=$((sub_pass + 1)) ;; fail) sub_fail=$((sub_fail + 1)) ;; skip) sub_skip=$((sub_skip + 1)) ;; esac
			printf '%s\t%s\t%s\tsubtest\t%s\n' "$module" "$package" "$subtest" "$sub_status" >>"$results"
		done < <(awk '$1 == "---" && ($2 == "PASS:" || $2 == "FAIL:" || $2 == "SKIP:") { status=$2; gsub(":", "", status); print tolower(status) "\t" $3 }' "$run_log")
	done <"$list_log"
done <"$packages_file"

if ((packages_enumerated != packages_compiled + compile_failures)); then
	echo "ERROR: package reconciliation mismatch: enumerated=$packages_enumerated compiled=$packages_compiled compile_failures=$compile_failures" >&2; status=1
fi
if ((discovered != executed)); then echo "ERROR: full-tier accounting mismatch: discovered=$discovered executed=$executed" >&2; status=1; fi
{
	printf 'summary\tpackages_enumerated\t%d\n' "$packages_enumerated"
	printf 'summary\tpackages_compiled\t%d\n' "$packages_compiled"
	printf 'summary\tpackages_no_test_files\t%d\n' "$packages_no_test_files"
	printf 'summary\tcompile_failures\t%d\n' "$compile_failures"
	printf 'summary\tdiscovery_failures\t%d\n' "$discovery_failures"
	printf 'summary\ttop_pass\t%d\n' "$top_pass"; printf 'summary\ttop_fail\t%d\n' "$top_fail"; printf 'summary\ttop_skip\t%d\n' "$top_skip"; printf 'summary\ttop_unrun\t%d\n' "$top_unrun"
	printf 'summary\tsubtest_pass\t%d\n' "$sub_pass"; printf 'summary\tsubtest_fail\t%d\n' "$sub_fail"; printf 'summary\tsubtest_skip\t%d\n' "$sub_skip"
	printf 'summary\tdiscovered\t%d\n' "$discovered"; printf 'summary\texecuted\t%d\n' "$executed"
} | tee -a "$results"
exit "$status"
