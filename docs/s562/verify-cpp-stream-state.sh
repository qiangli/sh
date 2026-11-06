#!/usr/bin/env bash
# Focused gate, including a baseline red. No corpus writes or broad suites.
set -euo pipefail
if [[ $# != 1 ]]; then
  echo 'usage: verify-cpp-stream-state.sh NEW_RESULTS' >&2
  exit 2
fi
mkdir "$1"
results=$(cd "$1" && pwd)
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
export GOWORK=off
python3 - <<'PYHASH'
import hashlib
from pathlib import Path
root=Path('internal/testdata/cpp-stream-state')
for line in (root/'SHA256SUMS').read_text().splitlines():
 digest,name=line.split()
 assert hashlib.sha256((root/name).read_bytes()).hexdigest()==digest,name
print('PASS exact retained fixture hashes')
PYHASH
git rev-parse HEAD > "$results/commit"
git diff --binary > "$results/worktree.patch"
clang++ --version > "$results/compiler"
go version > "$results/go-version"
git show 779c0127:polyglot/c.go > "$results/base-c.go"
python3 - "$root" "$results" <<'PY'
import json,sys
root,out=sys.argv[1:]
json.dump({'Replace':{root+'/polyglot/c.go':out+'/base-c.go'}},open(out+'/overlay.json','w'))
PY
if go test -overlay="$results/overlay.json" ./polyglot -run '^TestCPPWorkerStreamState/(empty|missing)$' -count=1 -v -timeout=90s > "$results/red.log" 2>&1; then
  echo 'FAIL: baseline unexpectedly passed' >&2; exit 1
fi
python3 - "$results/red.log" <<'PY'
import sys
s=open(sys.argv[1]).read()
assert s.count('C++ worker returned no result frame') == 2, 'baseline did not reach both missing-frame failures; inspect red.log'
for case in ['empty','missing']:
 assert '--- FAIL: TestCPPWorkerStreamState/'+case in s, 'missing baseline failure: '+case
PY
# The third retained example must reproduce the same transport failure too.
if go test -overlay="$results/overlay.json" ./polyglot -run '^TestCPPWorkerIfstreamCorpus/L49$' -count=1 -v -timeout=90s > "$results/red-L49.log" 2>&1; then
  echo 'FAIL: L49 baseline unexpectedly passed' >&2; exit 1
fi
python3 - "$results/red-L49.log" <<'PYRED'
import sys
s=open(sys.argv[1]).read()
assert s.count('C++ worker returned no result frame') == 1, 'L49 baseline did not reach missing-frame failure; inspect red-L49.log'
assert '--- FAIL: TestCPPWorkerIfstreamCorpus/L49' in s, 'missing L49 baseline failure'
PYRED
go test ./polyglot -run '^(TestCPPWorkerStreamState|TestCPPWorkerIfstreamCorpus|TestCWorkerLifecycle|TestCWorkerAtexitCorpus|TestCPPWorkerLifecycle|TestNativeWorkerResultFrames)$' -count=1 -json -timeout=2m > "$results/polyglot.json"
go test -tags full ./lower -run '^(TestCPPWorkerStreamStateLowered|TestCPPWorkerIfstreamCorpusLowered|TestCWorkerLifecycleLowered|TestCWorkerAtexitLowered|TestCPPWorkerLifecycleLowered|TestProgramMainClosesForeignWorkersBeforeExit|TestFoundationMainClosesForeignWorkersBeforeExit)$' -count=1 -json -timeout=4m > "$results/lower.json"
python3 - "$results" <<'PY'
import json,sys
from pathlib import Path
root=Path(sys.argv[1])
required={
 'polyglot': {'TestCPPWorkerStreamState','TestCPPWorkerIfstreamCorpus','TestCWorkerLifecycle','TestCWorkerAtexitCorpus','TestCPPWorkerLifecycle','TestNativeWorkerResultFrames'},
 'lower': {'TestCPPWorkerStreamStateLowered','TestCPPWorkerIfstreamCorpusLowered','TestCWorkerLifecycleLowered','TestCWorkerAtexitLowered','TestCPPWorkerLifecycleLowered','TestProgramMainClosesForeignWorkersBeforeExit','TestFoundationMainClosesForeignWorkersBeforeExit'}}
for package,names in required.items():
 events=[json.loads(s) for s in (root/(package+'.json')).read_text().splitlines()]
 assert not any(e['Action'] in ('skip','fail') for e in events),events
 passed={e.get('Test') for e in events if e['Action']=='pass'}
 assert names <= passed, names-passed
 assert None in passed, 'missing package PASS'
 prefix='TestCPPWorkerIfstreamCorpus'+('Lowered' if package=='lower' else '')
 assert {prefix+'/'+case for case in ['L38','L144','L49']} <= passed, 'missing exact fixture PASS'
 print('PASS',package,', '.join(sorted(names)))
print('PASS baseline reds, native/interface and interpreted/compiled stream-state and lifecycle controls')
PY
