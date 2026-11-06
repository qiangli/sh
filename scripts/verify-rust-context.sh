#!/bin/sh
# Bounded worker gate. Requires the exact test fixture crates already cached;
# provisioning uses cargo vendor --offline and cannot download missing inputs.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
logs=${1:?usage: verify-rust-context.sh NEW-LOG-DIRECTORY}
mkdir "$logs"
export BASHPP_RUST_CONTEXT_GATE=1
# Separate logs preserve process failures and reconcile named tests, not exit 0 alone.
go test -json -count=1 -timeout=120s ./polyglot -run '^TestRust(Context|LockedContext|NativeArtifactCallsAndDiagnostics)' > "$logs/polyglot.json"
go test -json -count=1 -timeout=120s -tags full ./lower -run '^(TestRustLockedContextCompiledWithoutAssets|TestEnvironmentLiteralCarriesEveryField)$' > "$logs/lower.json"
go test -json -race -count=1 -timeout=120s ./polyglot -run '^TestRustContext' > "$logs/race.json"
python3 - "$logs" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
required = {
 'polyglot': {'TestRustContextAdmission', 'TestRustContextDiagnosticClasses', 'TestRustContextRootModuleSnapshot', 'TestRustLockedContextArtifact', 'TestRustLockedContextSourceAndDependencyFailures', 'TestRustLockedContextPrewarm', 'TestRustLockedContextRootModules', 'TestRustNativeArtifactCallsAndDiagnostics'},
 'lower': {'TestRustLockedContextCompiledWithoutAssets', 'TestEnvironmentLiteralCarriesEveryField'},
 'race': {'TestRustContextAdmission', 'TestRustContextDiagnosticClasses', 'TestRustContextRootModuleSnapshot'},
}
for suite, names in required.items():
    events = [json.loads(line) for line in (root / (suite + '.json')).read_text().splitlines()]
    bad = [e for e in events if e.get('Action') in ('skip', 'fail')]
    passed = {e.get('Test') for e in events if e.get('Action') == 'pass'}
    package_pass = any(e.get('Action') == 'pass' and not e.get('Test') for e in events)
    if bad or not package_pass or not names <= passed:
        raise SystemExit(f'FAIL {suite}: bad={bad}, missing={names-passed}, package_pass={package_pass}')
    print(f'PASS {suite}: {len(passed-{None})} test/subtest passes, zero skips/failures; named denominator verified')
print('PASS locked Rust context gate; remote corpus acceptance is separate')
PY
