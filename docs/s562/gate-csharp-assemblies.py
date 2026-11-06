#!/usr/bin/env python3
"""Pinned Linux gate; inventory and commit marker staged from git archive."""
import argparse, hashlib, json, os, pathlib, shutil, subprocess
p = argparse.ArgumentParser()
for name in ('source', 'commit', 'go', 'go-sha256', 'pwsh', 'pwsh-sha256', 'scratch'):
    p.add_argument('--'+name, required=True)
p.add_argument('--cache-seed')
p.add_argument('--official-fixtures')
p.add_argument('--review-followup', action='store_true')
a = p.parse_args()
src = pathlib.Path(a.source).resolve()
def digest(path):
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()
assert (src/'.s562-source-commit').read_text().strip() == a.commit, 'wrong commit'
assert digest(a.go) == a.go_sha256, 'wrong Go provider'
assert digest(a.pwsh) == a.pwsh_sha256, 'wrong PowerShell provider'
for line in (src/'.s562-source-sha256').read_text().splitlines():
    sha, path = line.split('  ', 1)
    assert digest(src/path) == sha, 'source mismatch: '+path
work = pathlib.Path(a.scratch).resolve()
work.mkdir()
env = dict(os.environ)
for key, name in [('HOME','home'), ('GOPATH','gopath'), ('GOMODCACHE','gomod'), ('GOCACHE','go-build'), ('TMPDIR','tmp')]:
    target = work/name
    if a.cache_seed and name in ('gomod','go-build'):
        shutil.copytree(pathlib.Path(a.cache_seed)/name, target)
    else:
        target.mkdir()
    env[key] = str(target)
env.update(PATH=str(pathlib.Path(a.pwsh).parent)+':'+str(pathlib.Path(a.go).parent)+':/usr/bin:/bin', BASHPP_PWSH=a.pwsh, GOWORK='off', GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off', GOENV='off', POWERSHELL_TELEMETRY_OPTOUT='1')
steps = {'polyglot': ['TestCSharpGraph'+n for n in ('LoadContext','CanceledLoad','Validation','Snapshot','Runtime','ContentIdentity','Overlay','ExternAlias')]+['TestCSharpAnalyzeExportsByReflection','TestCSharpTypedCallsErrorsAndState','TestPowerShellPersistentCallsTypedValuesAndOutput'], 'lower': ['TestCSharpAssembliesBothModesWithoutSourceAssets','TestCSharpFenceInterpretedNativeParity','TestPowerShellFenceInterpretedNativeParity']}
if a.review_followup:
    steps['polyglot'] = ['TestCSharpGraphRuntime']
if a.official_fixtures:
    env['BASHPP_CSHARP_OFFICIAL_GRAPHS'] = a.official_fixtures
    steps['lower'].append('TestCSharpOfficialGraphsBothModes')
argv = [a.go,'list','-mod=readonly','-tags','full','-deps','-test','./polyglot','./lower']
with (work/'dependency-list.stdout').open('w') as out, (work/'dependency-list.stderr').open('w') as err:
    result = subprocess.run(argv,cwd=src,env=env,stdout=out,stderr=err,timeout=300)
assert result.returncode == 0, 'offline dependency closure failed'
for package, names in steps.items():
    argv = [a.go,'test','-json','-tags','full','-timeout=20m','./'+package,'-run','^('+'|'.join(names)+')$','-count=1']
    (work/(package+'.argv.json')).write_text(json.dumps(argv))
    with (work/(package+'.jsonl')).open('w') as out, (work/(package+'.stderr')).open('w') as err:
        result = subprocess.run(argv,cwd=src,env=env,stdout=out,stderr=err,timeout=1500)
    assert result.returncode == 0, package+' failed'
    events = [json.loads(line) for line in (work/(package+'.jsonl')).read_text().splitlines()]
    assert not any(e['Action'] in ('skip','fail') for e in events), 'skip/fail'
    for name in names:
        assert sum(e['Action']=='pass' and e.get('Test')==name for e in events)==1, 'missing PASS '+name
    assert any(e['Action']=='pass' and not e.get('Test') for e in events), 'missing package PASS'
    print('PASS',package,len(names),flush=True)
