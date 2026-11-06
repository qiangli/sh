#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 2 ]]; then
  echo 'usage: verify-foreign-shell-arguments.sh NEW_RESULTS C_CORPUS_ROOT' >&2
  exit 2
fi
mkdir "$1"
results=$(cd "$1" && pwd)
corpus=$(cd "$2" && pwd)
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
for suffix in bsh args expected; do
  cmp "internal/testdata/c-div/L72.$suffix" "$corpus/c-by-example/c-runtime-library/div/L72.$suffix"
done
git rev-parse HEAD > "$results/commit"
git diff --binary > "$results/worktree.patch"
go version > "$results/go-version"
clang --version > "$results/clang-version"
export GOWORK=off
git show 4de735cf:lower/callables_arguments.go > "$results/base-callables_arguments.go"
python3 - "$root" "$results" <<'PY'
import json,sys
root,out=sys.argv[1:]
json.dump({'Replace':{root+'/lower/callables_arguments.go':out+'/base-callables_arguments.go'}},open(out+'/overlay.json','w'))
PY
if go test -overlay="$results/overlay.json" -tags full ./lower -run '^TestForeignShellArgumentsCDiv$' -count=1 -timeout=3m > "$results/red.log" 2>&1; then
  echo 'FAIL: baseline unexpectedly passed' >&2; exit 1
fi
python3 - "$results/red.log" <<'PY'
import sys
s=open(sys.argv[1]).read()
assert 'strconv.ParseInt: parsing' in s and 'example_argc' in s and '--- FAIL: TestForeignShellArgumentsCDiv' in s,s
assert 'interpreted = {"x is 876, y is 13' in s,s
PY
go test -tags full ./lower -run '^TestForeignShellArguments' -count=1 -json -timeout=3m > "$results/green.json" || true
python3 - "$results/green.json" <<'PY'
import json,sys
events=[json.loads(s) for s in open(sys.argv[1])]
required={'TestForeignShellArguments','TestForeignShellArgumentsCDiv'}
required.update('TestForeignShellArguments/'+s for s in ('shell_scalar','shell_string','empty_and_unset','quoted_literal','typed_value','typed_string','shadowing','opt_in','invalid','reassignment'))
passed={e.get('Test') for e in events if e['Action']=='pass'}
assert not [e for e in events if e['Action'] in ('fail','skip')],events
assert not required-passed,required-passed
assert any(e['Action']=='pass' and 'Test' not in e for e in events)
print('PASS unchanged C division native/interpreted/compiled; baseline red; all',len(required),'required test events')
PY
