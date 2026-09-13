#!/bin/bash
# Sprint 162 / S162.2 step 1 — the cmd/compile/internal/abt spike (design
# evidence only; no product code). Measures, on one host, every route the
# design note weighs, on the smallest package root of the 26:
#
#   N   native `go test` of the package (the upstream baseline and enumeration)
#   R0  the backend's exact interpreted / compiled invocations on the base
#       candidate (reproduces the Barrier B first line by hand)
#   C1  compiled, overlay route: transpile the tested package as a library,
#       split the generated Go by origin file (by hand — the per-file emitter
#       is the product mechanism the note proposes), `-overlay` it over the
#       original files at the original import path, `go test` the original
#       enumeration; proof by `-x` (compile argv names the generated files)
#       and by a canary test that exists only in the generated file
#   C2  compiled, flat route: a driver derived from Go's own _testmain.go
#       (testing.Main instead of MainStart(testdeps…)) + the package map →
#       transpile → `go build` of the generated module → run
#   C3  compiled, importcfg route: the flat program WITH the real
#       _testmain.go body (testing/internal/testdeps) compiled by
#       `go tool compile -importcfg` + `go tool link` — the policy-free
#       compiler; proves internal imports need no GOROOT copy
#   I1  interpreted: the C2 driver + map through the interpreter (native
#       `testing`/`regexp` over the runtime import bridge), 60 s bound, then
#       unbounded once for the real number
#
# usage: sprint162-abt-spike.sh <out-dir> <go-binary> <bashy.real> <sh-source-root>
# Every command and its exit status, wall time and first lines are appended
# to <out-dir>/spike.log; raw outputs live under <out-dir>/<step>/.
set -u
out=${1:?out dir}; go=${2:?go}; bashy=${3:?bashy.real}; shrt=${4:?sh root}
mkdir -p "$out" || exit 2
out=$(cd "$out" && pwd)
log=$out/spike.log
export GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=2 GOCACHE=$out/gocache TMPDIR=$out/tmp
export BASHY_HINTS=0 BASHY_NO_COACH=1 BASHY_OTEL_SPOOL=$out/bashy-otel.jsonl
unset POSIXLY_CORRECT POSIX_PEDANTIC GOROOT
mkdir -p "$TMPDIR"
command -v timeout > /dev/null || timeout() { shift; "$@"; }   # darwin dry-run without coreutils
sha() { if command -v sha256sum > /dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -c1-16; }
pkg=cmd/compile/internal/abt

say() { printf '%s\n' "$*" | tee -a "$log"; }
# run <label> <timeout-seconds> <cmd...>: records wall time and exit status.
run() {
	label=$1; bound=$2; shift 2
	say "### $label"; say "\$ $*"
	start=$(date +%s.%N)
	timeout -k 5 "$bound" "$@" > "$out/$label.out" 2> "$out/$label.err"
	rc=$?
	end=$(date +%s.%N)
	wall=$(awk -v s="$start" -v e="$end" 'BEGIN { printf "%.2f", e - s }')
	say "exit=$rc wall=${wall}s (bound ${bound}s) stdout=$(wc -l < "$out/$label.out") lines stderr=$(wc -l < "$out/$label.err") lines"
	head -c 600 "$out/$label.err" | head -6 | sed 's/^/  err| /' | tee -a "$log"
	head -c 600 "$out/$label.out" | head -6 | sed 's/^/  out| /' | tee -a "$log"
}
terminal() { awk 'BEGIN{FS="\""} /"Action":"(pass|fail|skip)"/ && !/"Test":/ { for (i = 1; i <= NF; i++) if ($i == "Action") print $(i+2) }' "$1"; }
tests_run() { grep -c '"Action":"pass","Package":"[^"]*","Test":' "$1"; }

say "== sprint162 abt spike start $(date -u +%FT%TZ) on $(uname -sm) nproc=$(nproc 2>/dev/null || sysctl -n hw.ncpu)"
say "go: $("$go" version) sha256=$(sha(){ if command -v sha256sum > /dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -c1-16; }; sha "$go")"
say "bashy: $("$bashy" --version) sha256=$(sha "$bashy")"
say "sh runtime source: $(git -C "$shrt" rev-parse --short HEAD 2>/dev/null || echo '?') at $shrt"
real_goroot=$("$go" env GOROOT)
say "GOROOT (pinned tool): $real_goroot"

# Mirror the SDK by symlink (the package gate's shape: overlays may not
# replace files beneath GOMODCACHE, and the mirror keeps the SDK read-only).
mirror=$out/goroot
mkdir -p "$mirror"
for entry in "$real_goroot"/*; do ln -sfn "$entry" "$mirror/${entry##*/}"; done
export GOROOT=$mirror
go=$mirror/bin/go
src=$mirror/src/$pkg
say "package dir: $src files: $(ls "$src")"
say "package digest (matrix form): $(for f in "$src"/*.go; do printf '%s %s\n' "$(sha256sum "$f" 2>/dev/null | cut -d' ' -f1)" "${f##*/}"; done | sort -k2 | sha256sum 2>/dev/null | cut -d' ' -f1)"

# ---------------------------------------------------------------- N native
mkdir -p "$out/N"; cd "$mirror/src" || exit 2
run N-native-go-test-cold 600 "$go" test -count=1 -json "$pkg"
say "N cold terminal=$(terminal "$out/N-native-go-test-cold.out") tests-passed=$(tests_run "$out/N-native-go-test-cold.out")"
run N-native-go-test 600 "$go" test -count=1 -json "$pkg"
say "N warm terminal=$(terminal "$out/N-native-go-test.out") tests-passed=$(tests_run "$out/N-native-go-test.out")"
# Go's own _testmain.go: keep the work dir of a -c build.
run N-testmain-work 600 "$go" test -c -work -o "$out/N/abt.test" "$pkg"
work=$(sed -n 's/^WORK=//p' "$out/N-testmain-work.err" | head -1)
testmain=$work/b001/_testmain.go
test -r "$testmain" || { say "FATAL: no _testmain.go under $work"; exit 1; }
cp "$testmain" "$out/N/_testmain.go"
say "testmain: $testmain tests=$(grep -c '^	{"Test' "$testmain")"

# ---------------------------------------------------------------- R0 backend form
# Exactly bashpp_backend.go's argv (mode interpreted / compiled) on the base.
mkdir -p "$out/R0/bashpp"
files=$src/avlint32.go,$src/avlint32_test.go
run R0-interpreted 60 "$bashy" --bashpp --source=go --go-import-path "$pkg.test" --go-package "$pkg=$files" --go-file "$out/N/_testmain.go" -- -test.v
printf 'module bashpp_s1508\n\ngo 1.27\n\nrequire mvdan.cc/sh/v3 v3.13.1\nreplace mvdan.cc/sh/v3 => %s\n' "$shrt" > "$out/R0/bashpp/go.mod"
run R0-compiled-transpile 60 "$bashy" transpile --bashpp --source=go --go-import-path "$pkg.test" --go-package "$pkg=$files" --go-file "$out/N/_testmain.go" -o "$out/R0/bashpp/main.go" --map "$out/R0/bashpp/main.go.map"

# ---------------------------------------------------------------- C1 overlay route
mkdir -p "$out/C1/gen"
run C1-transpile-library 60 "$bashy" transpile --bashpp --source=go --go-import-path "$pkg" --go-file "$src/avlint32.go" --go-file "$src/avlint32_test.go" -o "$out/C1/gen/flat.go" --map "$out/C1/gen/flat.go.map"
if test -s "$out/C1/gen/flat.go"; then
	say "C1 generated: $(wc -l < "$out/C1/gen/flat.go") lines; runtime import: $(grep -c 'lower/shellrt' "$out/C1/gen/flat.go"); mangled names: $(grep -c '__gosource' "$out/C1/gen/flat.go")"
	# Split by origin file: every top-level declaration carries a //line
	# directive naming its source; the emitter proposed by the note does this
	# itself. Imports: each part keeps the imports its identifiers use.
	split_part() { # <origin-basename> <out-file>
		awk -v want="$1" '
			/^\/\/line / { file = $2; sub(/:.*/, "", file); n = split(file, a, "/"); cur = a[n]; next }
			/^\/\/ lower:/ || /^\/\/$/ { next }
			/^package / || /^import / { next }
			cur == want { print }
		' "$out/C1/gen/flat.go" > "$2.body"
		{
			printf '// Code generated by mvdan.cc/sh/v3/lower (spike hand-split by origin file). DO NOT EDIT.\n\npackage abt\n\n'
			for imp in fmt strconv strings testing; do
				grep -Eq "(^|[^A-Za-z0-9_])$imp\\." "$2.body" && printf 'import "%s"\n' "$imp"
			done
			printf '\n'
			cat "$2.body"
		} > "$2"
		rm -f "$2.body"
	}
	split_part avlint32.go "$out/C1/gen/avlint32.go"
	split_part avlint32_test.go "$out/C1/gen/avlint32_test.go"
	say "C1 split: $(wc -l < "$out/C1/gen/avlint32.go") + $(wc -l < "$out/C1/gen/avlint32_test.go") lines; decl counts orig/gen: $(grep -c '^func \|^type \|^var \|^const ' "$src/avlint32.go" "$src/avlint32_test.go" | tr '\n' ' ') / $(grep -c '^func \|^type \|^var \|^const ' "$out/C1/gen/avlint32.go" "$out/C1/gen/avlint32_test.go" | tr '\n' ' ')"
	printf '{"Replace":{"%s/avlint32.go":"%s/C1/gen/avlint32.go","%s/avlint32_test.go":"%s/C1/gen/avlint32_test.go"}}\n' "$src" "$out" "$src" "$out" > "$out/C1/overlay.json"
	run C1-go-test-overlay 600 "$go" test -count=1 -json -overlay="$out/C1/overlay.json" "$pkg"
	say "C1 terminal=$(terminal "$out/C1-go-test-overlay.out") tests-passed=$(tests_run "$out/C1-go-test-overlay.out")"
	run C1-go-test-overlay-warm 600 "$go" test -count=1 -json -overlay="$out/C1/overlay.json" "$pkg"
	say "C1 warm terminal=$(terminal "$out/C1-go-test-overlay-warm.out") tests-passed=$(tests_run "$out/C1-go-test-overlay-warm.out")"
	GOCACHE=$out/gocache-x run C1-go-test-overlay-x 600 "$go" test -count=1 -x -overlay="$out/C1/overlay.json" "$pkg"
	rm -rf "$out/gocache-x"
	# The compile argv for -p cmd/compile/internal/abt: its .go inputs must all
	# be generated files (the -trimpath rewrite maps them back to the original
	# names for positions, which is the point of the overlay).
	compile_inputs=$(grep "tool/[^ ]*/compile .* -p $pkg " "$out/C1-go-test-overlay-x.err" | head -1 | tr ' ' '\n' | grep '\.go$' | tr '\n' ' ')
	say "C1 proof (-x): compile inputs for -p $pkg: $compile_inputs"
	say "C1 proof (-x): generated inputs: $(printf '%s\n' $compile_inputs | grep -c "^$out/C1/gen/"); original inputs: $(printf '%s\n' $compile_inputs | grep -c "^$src/")"
	# Canary: a test that exists only in the generated file must be enumerated
	# and run by cmd/go's own _testmain.go under the overlay.
	mkdir -p "$out/C1/gen2"; cp "$out/C1/gen/avlint32.go" "$out/C1/gen2/avlint32.go"
	{ cat "$out/C1/gen/avlint32_test.go"; printf '\nfunc TestBashppOverlayCanary(t *testing.T) { t.Log("overlay-active: generated file compiled") }\n'; } > "$out/C1/gen2/avlint32_test.go"
	printf '{"Replace":{"%s/avlint32.go":"%s/C1/gen2/avlint32.go","%s/avlint32_test.go":"%s/C1/gen2/avlint32_test.go"}}\n' "$src" "$out" "$src" "$out" > "$out/C1/overlay2.json"
	run C1-canary 600 "$go" test -count=1 -v -run 'TestBashppOverlayCanary' -overlay="$out/C1/overlay2.json" "$pkg"
	say "C1 canary ran: $(grep -c 'overlay-active' "$out/C1-canary.out")"
	run C1-native-canary-negative 600 "$go" test -count=1 -v -run 'TestBashppOverlayCanary' "$pkg"
	say "C1 canary without overlay (must be 0): $(grep -c 'overlay-active' "$out/C1-native-canary-negative.out")"
fi

# ---------------------------------------------------------------- driver (C2, I1)
# Derived from Go's own _testmain.go: the same tests table, testing.Main in
# place of testing.MainStart(testdeps.TestDeps{}, …). A spike instrument.
mkdir -p "$out/C2/bashpp" "$out/I1"
{
	printf 'package main\n\nimport (\n\t"regexp"\n\t"testing"\n\n\t_test "%s"\n)\n\n' "$pkg"
	awk '/^var tests = /,/^}/' "$out/N/_testmain.go"
	printf '\nfunc match(pat, str string) (bool, error) { return regexp.MatchString(pat, str) }\n\nfunc main() { testing.Main(match, tests, nil, nil) }\n'
} > "$out/C2/driver.go"
say "driver: $(grep -c '^	{"Test' "$out/C2/driver.go") tests"
printf 'module bashpp_s1508\n\ngo 1.27\n\nrequire mvdan.cc/sh/v3 v3.13.1\nreplace mvdan.cc/sh/v3 => %s\n' "$shrt" > "$out/C2/bashpp/go.mod"
cp "$shrt/go.sum" "$out/C2/bashpp/go.sum" 2>/dev/null
run C2-transpile 60 "$bashy" transpile --bashpp --source=go --go-import-path "$pkg.test" --go-package "$pkg=$files" --go-file "$out/C2/driver.go" -o "$out/C2/bashpp/main.go" --map "$out/C2/bashpp/main.go.map"
if test -s "$out/C2/bashpp/main.go"; then
	say "C2 generated: $(wc -l < "$out/C2/bashpp/main.go") lines; imports: $(grep '^import' "$out/C2/bashpp/main.go" | tr '\n' ' ')"
	run C2-go-build 600 "$go" build -C "$out/C2/bashpp" -o "$out/C2/program" .
	test -x "$out/C2/program" && run C2-run 600 "$out/C2/program" -test.v
	say "C2 passed tests: $(grep -c '^--- PASS' "$out/C2-run.out") failed: $(grep -c '^--- FAIL' "$out/C2-run.out") final: $(tail -1 "$out/C2-run.out")"
fi

# ---------------------------------------------------------------- C3 importcfg route
# The flat program with the REAL _testmain.go body (testing/internal/testdeps),
# built by the policy-free compiler: importcfg from `go list -export -deps`
# in the generated module (all targets are command-line packages, so cmd/go's
# internal rule does not apply to them), then compile + link.
mkdir -p "$out/C3/bashpp"
if test -s "$out/C2/bashpp/main.go"; then
	cp "$out/C2/bashpp/go.mod" "$out/C3/bashpp/go.mod"; cp "$out/C2/bashpp/go.sum" "$out/C3/bashpp/go.sum" 2>/dev/null
	# Replace the driver's main/imports by the _testmain.go ones: the tests
	# table is identical (same generator), only the entry differs.
	# The program's own declarations precede the mapped package's in the
	# generated file, so drop exactly the driver's two one-line functions.
	grep -v '^func match(\|^func main() { testing.Main' "$out/C2/bashpp/main.go" | sed 's/^import "regexp"$/import "os"\nimport "testing\/internal\/testdeps"/' > "$out/C3/bashpp/main.go"
	awk '/^var benchmarks = /,0' "$out/N/_testmain.go" | grep -v '^$' >> "$out/C3/bashpp/main.go"
	gofmt -l "$out/C3/bashpp/main.go" > /dev/null 2>&1 || say "C3 note: hand-assembled main.go is not gofmt-clean (spike only)"
	imports=$(sed -n 's/^import \(.* \)\{0,1\}"\(.*\)"$/\2/p' "$out/C3/bashpp/main.go" | sort -u | tr '\n' ' ')
	say "C3 imports: $imports"
	(cd "$out/C3/bashpp" && run C3-go-build-negative 600 "$go" build -o "$out/C3/program-gobuild" .)
	say "C3 go build (expected: internal refusal): $(grep -c 'use of internal package' "$out/C3-go-build-negative.err")"
	(cd "$out/C3/bashpp" && run C3-go-list-importcfg 600 "$go" list -export -deps -f '{{if .Export}}packagefile {{.ImportPath}}={{.Export}}{{end}}' $imports)
	grep -v '^$' "$out/C3-go-list-importcfg.out" > "$out/C3/importcfg"
	say "C3 importcfg: $(wc -l < "$out/C3/importcfg") packagefile lines; testdeps: $(grep -c '^packagefile testing/internal/testdeps=' "$out/C3/importcfg"); shellrt: $(grep -c 'shellrt' "$out/C3/importcfg")"
	(cd "$out/C3/bashpp" && run C3-compile 600 "$go" tool compile -p main -importcfg "$out/C3/importcfg" -o "$out/C3/main.o" main.go)
	test -s "$out/C3/main.o" && run C3-link 600 "$go" tool link -importcfg "$out/C3/importcfg" -o "$out/C3/program" "$out/C3/main.o"
	test -x "$out/C3/program" && run C3-run 600 "$out/C3/program" -test.v
	say "C3 passed tests: $(grep -c '^--- PASS' "$out/C3-run.out" 2>/dev/null) failed: $(grep -c '^--- FAIL' "$out/C3-run.out" 2>/dev/null) final: $(tail -1 "$out/C3-run.out" 2>/dev/null)"
fi

# ---------------------------------------------------------------- I1 interpreted
run I1-interpreted-60s 60 "$bashy" --bashpp --source=go --go-import-path "$pkg.test" --go-package "$pkg=$files" --go-file "$out/C2/driver.go" -- -test.v
say "I1 (60 s) passed tests: $(grep -c '^--- PASS' "$out/I1-interpreted-60s.out") failed: $(grep -c '^--- FAIL' "$out/I1-interpreted-60s.out") last: $(tail -1 "$out/I1-interpreted-60s.out")"
if ! grep -q '^PASS$\|^FAIL$' "$out/I1-interpreted-60s.out"; then
	run I1-interpreted-unbounded 1800 "$bashy" --bashpp --source=go --go-import-path "$pkg.test" --go-package "$pkg=$files" --go-file "$out/C2/driver.go" -- -test.v
	say "I1 (unbounded) passed tests: $(grep -c '^--- PASS' "$out/I1-interpreted-unbounded.out") failed: $(grep -c '^--- FAIL' "$out/I1-interpreted-unbounded.out") last: $(tail -1 "$out/I1-interpreted-unbounded.out")"
fi
# One test at a time, to see per-test cost under the interpreter.
for t in TestBounds TestEquals TestApplicInsert; do
	run "I1-interpreted-$t" 300 "$bashy" --bashpp --source=go --go-import-path "$pkg.test" --go-package "$pkg=$files" --go-file "$out/C2/driver.go" -- -test.v -test.run "^$t\$"
done

# ---------------------------------------------------------------- S scale (all 26)
# The compiled overlay route's first gate for every package root: does the
# tested package (GoFiles + TestGoFiles, one build unit, the same shape C1
# used) check and lower as a library through the base candidate, and how
# long does it take? External test packages (XTestGoFiles) are listed but
# lowered separately (they import the tested package by path). A number per
# package, not a verdict: `go test` is not run here.
mkdir -p "$out/S"
say "### S scale: transpile-library of every package root (bound 600 s each)"
matrix_pkgs="cmd/compile cmd/compile/internal/abt cmd/compile/internal/amd64 cmd/compile/internal/base cmd/compile/internal/compare cmd/compile/internal/devirtualize cmd/compile/internal/dwarfgen cmd/compile/internal/importer cmd/compile/internal/inline/inlheur cmd/compile/internal/ir cmd/compile/internal/liveness cmd/compile/internal/logopt cmd/compile/internal/loopvar cmd/compile/internal/noder cmd/compile/internal/rangefunc cmd/compile/internal/reflectdata cmd/compile/internal/ssa cmd/compile/internal/ssagen cmd/compile/internal/syntax cmd/compile/internal/test cmd/compile/internal/typecheck cmd/compile/internal/types cmd/compile/internal/types2 cmd/internal/testdir go/types internal/types/errors"
printf 'package\tgo\ttest\txtest\ts\tloc\texit\twall_s\tfirst_line\n' > "$out/S/scale.tsv"
for p in $matrix_pkgs; do
	id=$(printf '%s' "$p" | tr '/.' '__')
	# The real SDK tree, not the symlink mirror: the base candidate decides
	# `internal` visibility from directories (lower/module_importer.go
	# isDirInGOROOT), and a symlink-mirrored GOROOT makes it refuse every
	# top-level internal import (measured on darwin; a venue artifact the
	# note records). The overlay route itself (C1) is unaffected.
	eval "$(GOROOT=$real_goroot "$real_goroot/bin/go" list -f 'pdir={{.Dir}}; gofiles="{{join .GoFiles " "}}"; testfiles="{{join .TestGoFiles " "}}"; xtestfiles="{{join .XTestGoFiles " "}}"; sfiles="{{join .SFiles " "}}"' "$p")"
	args=
	for f in $gofiles $testfiles; do args="$args --go-file $pdir/$f"; done
	loc=$(cat $(for f in $gofiles $testfiles; do printf '%s/%s ' "$pdir" "$f"; done) | wc -l | tr -d ' ')
	if test -z "$gofiles$testfiles"; then
		printf '%s\t%s\t%s\t%s\t%s\t%s\t-\t-\tno GoFiles/TestGoFiles (xtest only)\n' "$p" "$(echo $gofiles | wc -w)" "$(echo $testfiles | wc -w)" "$(echo $xtestfiles | wc -w)" "$(echo $sfiles | wc -w)" "$loc" >> "$out/S/scale.tsv"
		continue
	fi
	start=$(date +%s.%N)
	# shellcheck disable=SC2086
	GOROOT=$real_goroot timeout -k 5 600 "$bashy" transpile --bashpp --source=go --go-import-path "$p" $args -o "$out/S/$id.go" --map "$out/S/$id.go.map" > "$out/S/$id.out" 2> "$out/S/$id.err"
	rc=$?
	wall=$(awk -v s="$start" -v e="$(date +%s.%N)" 'BEGIN { printf "%.1f", e - s }')
	first=$(head -1 "$out/S/$id.err" | cut -c1-200)
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$p" "$(echo $gofiles | wc -w)" "$(echo $testfiles | wc -w)" "$(echo $xtestfiles | wc -w)" "$(echo $sfiles | wc -w)" "$loc" "$rc" "$wall" "$first" >> "$out/S/scale.tsv"
	say "S $p exit=$rc wall=${wall}s loc=$loc :: $first"
	rm -f "$out/S/$id.go" "$out/S/$id.go.map"
done
say "S summary: $(awk -F '\t' 'NR > 1 && $7 == 0 { ok++ } NR > 1 { n++ } END { printf "%d/%d transpile ok", ok, n }' "$out/S/scale.tsv")"

say "survivors under $out: $(ps -eo args | grep -F "$out" | grep -v grep | wc -l)"
rm -rf "$out/gocache"
say "== sprint162 abt spike end $(date -u +%FT%TZ)"
