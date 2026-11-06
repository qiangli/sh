#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 2 ]]; then
  echo 'usage: verify-python-scalars.sh NEW_RESULTS CORPUS_ROOT' >&2
  exit 2
fi
mkdir "$1"
results=$(cd "$1" && pwd)
corpus=$(cd "$2" && pwd)
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
for suffix in bsh expected; do
  cmp "internal/testdata/python-scalars/scalars.$suffix" "$corpus/python-by-example/00-boundary/scalars.$suffix"
done
git rev-parse HEAD > "$results/commit"
git diff --binary > "$results/worktree.patch"
go version > "$results/go-version"
python3 --version > "$results/python-version"
export GOWORK=off
git show 779c0127:lower/polyglot.go > "$results/base-polyglot.go"
git show 779c0127:lower/compile.go > "$results/base-compile.go"
python3 - "$root" "$results" <<'PY'
import json,sys
root,out=sys.argv[1:]
json.dump({'Replace':{root+'/lower/polyglot.go':out+'/base-polyglot.go',root+'/lower/compile.go':out+'/base-compile.go'}},open(out+'/overlay.json','w'))
PY
if go test -overlay="$results/overlay.json" -tags full ./lower -run '^TestForeignScalarArguments$/^corpus$/^static$' -count=1 -timeout=3m > "$results/red.log" 2>&1; then
  echo 'FAIL: baseline unexpectedly passed' >&2; exit 1
fi
python3 - "$results/red.log" <<'PY'
import sys
s=open(sys.argv[1]).read()
assert 'LOWER-ETYPE' in s and 'untyped string constant "2.5"' in s and '--- FAIL: TestForeignScalarArguments/corpus/static' in s,s
PY
# The original typed function-value contract must pass on the baseline too.
go test -overlay="$results/overlay.json" -tags full ./lower -run '^TestForeignScalarFunctionValues$' -count=1 -json -timeout=3m > "$results/baseline-values.json"
# Collect all focused results even when one gate fails; the reconciliation is fatal.
go test ./lower/shellrt -run '^(TestForeignScalarArgument|TestForeignBytesProjection)$' -count=1 -json > "$results/shellrt.json" || true
go test -tags full ./lower -run '^(TestForeignScalarFunctionValues|TestForeignScalarArguments|TestForeignScalarArgumentSingleEvaluation|TestForeignBytesResults|TestForeignVariadicScalarParameterTypes|TestForeignErrorOptInPython|TestPythonImportObjectCallsLowerThroughSharedRuntime)$' -count=1 -json -timeout=5m > "$results/lower.json" || true
go test ./polyglot -run '^TestPythonCommandCancellation$' -count=1 -json -timeout=1m > "$results/polyglot.json" || true
python3 - "$results" <<'PY'
import json,sys
from pathlib import Path
root=Path(sys.argv[1])
required={
 'shellrt':{'TestForeignScalarArgument','TestForeignBytesProjection'},
 'baseline-values':{'TestForeignScalarFunctionValues','TestForeignScalarFunctionValues/static','TestForeignScalarFunctionValues/execution'},
 'lower':{'TestForeignScalarFunctionValues','TestForeignScalarFunctionValues/static','TestForeignScalarFunctionValues/execution','TestForeignBytesResults','TestForeignBytesResults/static','TestForeignBytesResults/execution','TestForeignVariadicScalarParameterTypes','TestForeignScalarArguments','TestForeignScalarArgumentSingleEvaluation','TestForeignErrorOptInPython','TestPythonImportObjectCallsLowerThroughSharedRuntime',
          'TestForeignScalarArguments/corpus/static','TestForeignScalarArguments/corpus/execution','TestForeignScalarArguments/native_Python'},
 'polyglot':{'TestPythonCommandCancellation'}}
for case in ('float_value','numeric_strings','invalid_opt_in','invalid_ordinary'):
 for mode in ('static','execution'):
  required['lower'].add('TestForeignScalarArguments/'+case+'/'+mode)
for mode in ('static','execution'):
 required['lower'].add('TestForeignScalarArgumentSingleEvaluation/'+mode)
failures=[]
for package,names in required.items():
 events=[json.loads(s) for s in (root/(package+'.json')).read_text().splitlines()]
 failed=[e.get('Test',package) for e in events if e['Action'] in ('skip','fail')]
 passed={e.get('Test') for e in events if e['Action']=='pass'}
 missing=names-passed
 if failed or missing or not any(e['Action']=='pass' and 'Test' not in e for e in events):
  failures.append((package,failed,sorted(missing)))
 else: print('PASS',package,', '.join(sorted(names)))
assert not failures,failures
print('PASS baseline red; exact corpus; native Python/interpreted/compiled scalar parity; cancellation')
PY
