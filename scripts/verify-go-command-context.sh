#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
command -v go >/dev/null

log=$(mktemp)
trap 'rm -f "$log"' EXIT

go test ./polyglot -run '^(TestGoCommandInvocationBoundary|TestGoCommandStreamsBeforeExitAndCancels|TestGoCommandSignalAfterReadiness|TestGoCommandCallerContextsAreIsolated|TestGoArtifactUsesModuleOverlayAndBridgesValues)$' -count=1 -v | tee "$log"
go test -tags full ./interp ./lower -run '^(TestBashPPGoCommandAndTypedCallsStayDistinct|TestBashPPGoCommandPreservesCallerEnvironment|TestBashPPGoCommandEnvironmentScopeRestoration|TestGoCommandInterpretedNativeParity|TestGoCommandUsesCurrentShellContextInBothModes)$' -count=1 -v | tee -a "$log"
go test -race -tags full ./interp -run '^TestBashPPGoCommandEnvironmentScopeRestoration$' -count=1 -v | tee -a "$log"

for name in \
  TestGoCommandInvocationBoundary \
  TestGoCommandStreamsBeforeExitAndCancels \
  TestGoCommandCallerContextsAreIsolated \
  TestGoArtifactUsesModuleOverlayAndBridgesValues \
  TestBashPPGoCommandAndTypedCallsStayDistinct \
  TestBashPPGoCommandPreservesCallerEnvironment \
  TestBashPPGoCommandEnvironmentScopeRestoration \
  TestGoCommandInterpretedNativeParity; do
  grep -q "^--- PASS: ${name} " "$log"
done
grep -q '^--- PASS: TestGoCommandUsesCurrentShellContextInBothModes ' "$log"
if [[ $(grep -c '^--- PASS: TestBashPPGoCommandEnvironmentScopeRestoration ' "$log") != 2 ]]; then
  echo 'Go command environment race gate did not pass' >&2
  exit 1
fi
if [[ $(go env GOOS) != windows ]]; then
  grep -q '^--- PASS: TestGoCommandSignalAfterReadiness ' "$log"
fi
if grep -Eq '^--- (FAIL|SKIP):|^FAIL([[:space:]]|$)' "$log"; then
  echo 'Go command context gate contains a failure or skip' >&2
  exit 1
fi
echo 'Go command context focused gate: named tests passed; zero failures/skips'
