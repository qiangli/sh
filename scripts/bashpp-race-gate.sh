#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

evidence=${BASHPP_RACE_EVIDENCE:-artifacts/bashpp-race-gate.txt}
mkdir -p "$(dirname "$evidence")"
: >"$evidence"
log() { printf '%s\n' "$*" | tee -a "$evidence"; }

go_bin=${GO:-go}
go_path=$(command -v "$go_bin")
go_dir=$(dirname "$go_path")
export PATH="/bin:/usr/bin:$go_dir:$PATH"

mapfile -t packages < <("$go_bin" list ./... | sort)
if ((${#packages[@]} == 0)); then
	log "ERROR: go list ./... returned zero packages"
	exit 1
fi

test_count=0
for pkg in "${packages[@]}"; do
	count=$("$go_bin" test -list '^(Test|Example|Fuzz)' "$pkg" | grep -Ec '^(Test|Example|Fuzz)' || true)
	test_count=$((test_count + count))
done
if ((test_count == 0)); then
	log "ERROR: test discovery found zero test/example/fuzz targets"
	exit 1
fi

log "Bash++ race/lifecycle gate"
log "go_version: $("$go_bin" version)"
log "goos: $("$go_bin" env GOOS)"
log "goarch: $("$go_bin" env GOARCH)"
log "package_count: ${#packages[@]}"
log "test_count: $test_count"
log "real_bash_compatibility_corpus: TestRunnerRunConfirm (separate confirm task)"
log "packages:"
printf '  %s\n' "${packages[@]}" | tee -a "$evidence"

log ""
log "oracle_audit: scripts/bashpp-test-oracle-audit.sh"
./scripts/bashpp-test-oracle-audit.sh 2>&1 | tee -a "$evidence"

focused_re='Test(Concurrency|JobCarrier|Signal|.*Cancel|.*Background|.*FIFO|.*Pipe)'
for procs in 1 2 4; do
	log ""
	log "focused: GOMAXPROCS=$procs go test -race -count=3 -run '$focused_re' ./interp"
	GOMAXPROCS=$procs "$go_bin" test -race -count=3 -run "$focused_re" ./interp 2>&1 | tee -a "$evidence"
done

log ""
log "race_all: go test -race ./... -skip 'TestRunnerRunConfirm|TestParseConfirm'"
"$go_bin" test -race ./... -skip 'TestRunnerRunConfirm|TestParseConfirm' 2>&1 | tee -a "$evidence"
log ""
log "gate: PASS"
