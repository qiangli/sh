#!/usr/bin/env bash
# The release test tier. Build each package's full-tag test binary once, then
# run every top-level test, example, and regular fuzz seed corpus in a fresh
# process. A panic therefore fails only its selected child; it cannot prevent
# later names from being run and recorded.
set -uo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
timeout=${TEST_FULL_TIMEOUT:-30m}
workdir=${FULL_TEST_WORKDIR:-}
evidence=${FULL_TEST_EVIDENCE_DIR:-"$root/artifacts/full-test"}
keep_workdir=0

usage() {
	echo "usage: $0 [--keep-workdir]" >&2
}

for arg in "$@"; do
	case "$arg" in
		--keep-workdir) keep_workdir=1 ;;
		*) usage; exit 2 ;;
	esac
done

if [ -z "$workdir" ]; then
	workdir=$(mktemp -d "${TMPDIR:-/tmp}/sh-full-test.XXXXXX")
	remove_workdir=1
else
	mkdir -p "$workdir"
	remove_workdir=0
fi
mkdir -p "$evidence"

cleanup() {
	if ((remove_workdir && !keep_workdir)); then
		rm -rf "$workdir"
	else
		echo "full-test workdir: $workdir"
	fi
}
trap cleanup EXIT

packages_file=$workdir/packages.tsv
inventory=$evidence/inventory.tsv
results=$evidence/results.tsv
: >"$packages_file"
printf 'module\tpackage\tdirectory\ttest_go_files\tx_test_go_files\n' >"$inventory"
printf 'module\tpackage\tname\tkind\tstatus\n' >"$results"

status=0
discovered=0
executed=0

list_packages() {
	local module=$1 directory=$2
	(
		cd "$directory"
		go list -tags full -f '{{.ImportPath}}|{{.Dir}}|{{len .TestGoFiles}}|{{len .XTestGoFiles}}' ./...
	) | while IFS='|' read -r package package_dir test_go_files x_test_go_files; do
		printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$module" "$directory" "$package" "$package_dir" "$test_go_files" "$x_test_go_files" >>"$packages_file"
		done
}

if ! list_packages root "$root"; then
	echo 'ERROR: could not enumerate root module packages' >&2
	exit 1
fi
if ! list_packages moreinterp "$root/moreinterp"; then
	echo 'ERROR: could not enumerate moreinterp packages' >&2
	exit 1
fi

# A sequential package number avoids depending on import paths being safe file
# names and makes the retained command/log paths easy to correlate with rows.
number=0
while IFS=$'\t' read -r module directory package package_dir test_go_files x_test_go_files; do
	number=$((number + 1))
	binary=$workdir/$number.test
	compile_log=$workdir/$number.compile.log
	printf '%s\t%s\t%s\t%s\t%s\n' "$module" "$package" "$package_dir" "$test_go_files" "$x_test_go_files" >>"$inventory"

	if ! (cd "$directory" && go test -tags full -timeout="$timeout" -c -o "$binary" "$package") >"$compile_log" 2>&1; then
		cat "$compile_log" >&2
		printf '%s\t%s\t-\tcompile\tfail\n' "$module" "$package" >>"$results"
		status=1
		continue
	fi

	# Packages without test sources are still compiled, as go test would compile
	# them, but do not produce a runnable test binary.
	if ((test_go_files == 0 && x_test_go_files == 0)); then
		printf '%s\t%s\t-\tpackage\tno-test-files\n' "$module" "$package" >>"$results"
		continue
	fi

	list_log=$workdir/$number.list.log
	if ! "$binary" -test.list=. >"$list_log" 2>&1; then
		cat "$list_log" >&2
		printf '%s\t%s\t-\tdiscovery\tfail\n' "$module" "$package" >>"$results"
		status=1
		continue
	fi

	while IFS= read -r name; do
		case "$name" in
			Test*) kind=test ;;
			Example*) kind=example ;;
			Fuzz*) kind=fuzz ;;
			*) continue ;;
		esac
		discovered=$((discovered + 1))
		printf '%s\t%s\t%s\t%s\tdiscovered\n' "$module" "$package" "$name" "$kind" >>"$results"
		run_log=$workdir/$number.$discovered.run.log
		# -test.v records subtests in the retained child log. Use an anchored
		# selector so a similarly named test cannot accidentally run too.
		if (cd "$package_dir" && "$binary" -test.timeout="$timeout" -test.v -test.run="^${name}$") >"$run_log" 2>&1; then
			run_status=pass
		else
			run_status=fail
			status=1
			cat "$run_log" >&2
		fi
		executed=$((executed + 1))
		printf '%s\t%s\t%s\t%s\t%s\n' "$module" "$package" "$name" "$kind" "$run_status" >>"$results"
		# Top-level selection runs all of its subtests in that same fresh child.
		# Retain their names as observed coverage without treating them as separate
		# processes (the release contract is one process per top-level test).
		while IFS= read -r subtest; do
			[ "$subtest" = "$name" ] && continue
			printf '%s\t%s\t%s\tsubtest\tobserved\n' "$module" "$package" "$subtest" >>"$results"
		done < <(sed -n 's/^=== RUN   //p' "$run_log")
	done <"$list_log"
done <"$packages_file"

if ((discovered != executed)); then
	echo "ERROR: full-tier accounting mismatch: discovered=$discovered executed=$executed" >&2
	status=1
fi
printf 'full-tier accounting: discovered=%d executed=%d\n' "$discovered" "$executed" | tee -a "$results"

exit "$status"
