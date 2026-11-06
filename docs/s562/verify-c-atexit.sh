#!/usr/bin/env bash
# Manager executes in a private candidate checkout; never modifies the corpus.
set -euo pipefail
if [[ $# != 2 ]]; then
  echo 'usage: verify-c-atexit.sh NEW_RESULTS CORPUS_ROOT' >&2
  exit 2
fi
results=$1
corpus=$2
mkdir "$results" # must be new; never reuse another worker's path
results=$(cd "$results" && pwd)
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
command -v clang > "$results/clang-path"
clang++ --version > "$results/clangxx-version"
clang --version > "$results/clang-version"
go version > "$results/go-version"
git rev-parse HEAD > "$results/commit"
git diff --binary > "$results/worktree.patch"
fixture=c-runtime-library/atexit/L53
cmp "internal/testdata/c-atexit/L53.bsh" "$corpus/$fixture.bsh"
cmp "internal/testdata/c-atexit/L53.expected" "$corpus/$fixture.expected"
export GOWORK=off
# The original implementation must fail for the original reason, using the
# current regression test and an overlay rather than modifying this checkout.
git show 68816971:polyglot/c.go > "$results/base-c.go"
python3 - "$root" "$results" <<'PY'
import json,sys
from pathlib import Path
root,out=sys.argv[1:]
# Keep the new decoder unit-test symbol available; baseline call path stays unchanged.
base=out+'/base-c.go'
current=Path(root+'/polyglot/c.go').read_text()
with open(base,'a') as f: f.write('\n'+current[current.index('func decodeNativeResult'):])
json.dump({'Replace':{root+'/polyglot/c.go':out+'/base-c.go'}},open(out+'/overlay.json','w'))
PY
if go test -overlay="$results/overlay.json" ./polyglot -run '^(TestCWorkerAtexitCorpus|TestCPPWorkerLifecycle)$' -count=1 -v > "$results/red.log" 2>&1; then
  echo 'FAIL: baseline unexpectedly passed' >&2; exit 1
fi
python3 - "$results/red.log" <<'PY'
import sys
s=open(sys.argv[1]).read()
assert 'invalid C worker result payload' in s and '--- FAIL: TestCWorkerAtexitCorpus' in s,s
assert 'invalid C++ worker result payload' in s and '--- FAIL: TestCPPWorkerLifecycle/return' in s,s
PY
go test ./polyglot -run '^(TestCWorker.*|TestCPPWorker.*|TestNativeWorkerResultFrames|TestCNativeArtifactCalls|TestCPPNativeArtifactCallsAndExceptions|TestCFileScopeDeclarationsRemainPrivate|TestCGenericSelectionUsesC17InWorker)$' -count=1 -json -timeout=2m > "$results/polyglot.json"
go test -tags full ./lower -run '^Test(C|CPP)Worker' -count=1 -json -timeout=4m > "$results/lower.json"
python3 - "$results" <<'PY'
import json,sys
from pathlib import Path
root=Path(sys.argv[1])
required={
 'polyglot':{'TestCWorkerAtexitCorpus','TestCWorkerLifecycle','TestCPPWorkerLifecycle','TestNativeWorkerResultFrames','TestCNativeArtifactCalls','TestCPPNativeArtifactCallsAndExceptions','TestCFileScopeDeclarationsRemainPrivate','TestCGenericSelectionUsesC17InWorker'},
 'lower':{'TestCWorkerAtexitLowered','TestCWorkerLifecycleLowered','TestCPPWorkerLifecycleLowered'}}
for package,names in required.items():
 events=[json.loads(s) for s in (root/(package+'.json')).read_text().splitlines()]
 assert not any(e['Action'] in ('skip','fail') for e in events),events
 passed={e.get('Test') for e in events if e['Action']=='pass'}
 assert names <= passed,(names-passed)
 assert any(e['Action']=='pass' and 'Test' not in e for e in events)
 print('PASS',package,', '.join(sorted(names)))
print('PASS baseline red, exact 47-byte native golden, interpreted/compiled lifecycle gates')
PY
