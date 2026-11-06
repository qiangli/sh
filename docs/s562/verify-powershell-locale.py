#!/usr/bin/env python3
"""Bounded gate: require named PASS events, no skips, native byte equality."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--go', required=True, type=Path)
p.add_argument('--pwsh', required=True, type=Path)
p.add_argument('--evidence', required=True, type=Path)
p.add_argument('--expected-commit', help='reject a different source revision')
p.add_argument('--corpus', action='store_true', help='include the exact 16 source-bound recipes (Linux acceptance)')
a = p.parse_args()
root = Path(__file__).resolve().parents[2]
go, pwsh = a.go.resolve(strict=True), a.pwsh.resolve(strict=True)
evidence = a.evidence.resolve()
evidence.mkdir(parents=True, exist_ok=False)
env = dict(os.environ, BASHPP_PWSH=str(pwsh), PATH=str(pwsh.parent)+os.pathsep+os.environ.get('PATH',''), GOWORK='off', GOTOOLCHAIN='local')
if (root/'.git').exists():
    head = subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()
    dirty = subprocess.check_output(['git','status','--porcelain'],cwd=root,text=True)
else:
    head = (root/'.s562-source-commit').read_text().strip()
    dirty = ''
    for line in (root/'.s562-source-sha256').read_text().splitlines():
        digest, name = line.split('  ',1)
        assert hashlib.sha256((root/name).read_bytes()).hexdigest()==digest, name
if a.expected_commit and (head != a.expected_commit or dirty):
    raise SystemExit('FAIL source revision or dirty checkout')
receipt = {'platform': platform.platform(), 'go': str(go), 'pwsh': str(pwsh),
           'go_sha256': hashlib.sha256(go.read_bytes()).hexdigest(),
           'pwsh_sha256': hashlib.sha256(pwsh.read_bytes()).hexdigest(),
           'head': head, 'dirty': dirty}
(evidence/'identity.json').write_text(json.dumps(receipt,indent=2)+'\n')
required = {
 'polyglot': ['TestPowerShellExplicitLocaleEnvironment','TestPowerShellExecutionEnvironment',
              'TestPowerShellExplicitInvariantOverridesProvisioner','TestDiscoverPowerShellEnvironmentCarriesProvisionedRuntimeControls'],
 'lower': ['TestPowerShellLocaleNativeInterpretedCompiled','TestCSharpLocaleNativeInterpretedCompiled','TestCSharpLocaleInterpolationRecipe']}
if a.corpus:
    required['lower'].append('TestPowerShellLocaleRecipeCorpus')
for package, names in required.items():
    cmd = [str(go),'test','-json','-tags','full','-count=1','-timeout=5m','./'+package,'-run','^('+'|'.join(names)+')$']
    result = subprocess.run(cmd,cwd=root,env=env,capture_output=True,timeout=330)
    (evidence/(package+'.jsonl')).write_bytes(result.stdout)
    (evidence/(package+'.stderr')).write_bytes(result.stderr)
    (evidence/(package+'.status')).write_text(str(result.returncode)+'\n')
    events = [json.loads(line) for line in result.stdout.splitlines()]
    passes = {e.get('Test') for e in events if e.get('Action')=='pass'}
    bad = [e for e in events if e.get('Action') in ('skip','fail')]
    if result.returncode or bad or not set(names).issubset(passes) or None not in passes:
        raise SystemExit('FAIL '+package+': missing PASS, skip, failure or incomplete package; inspect '+str(evidence))
    if package=='lower':
        for locale in ('en_US.UTF-8','de_DE.UTF-8'):
            assert 'TestPowerShellLocaleNativeInterpretedCompiled/'+locale in passes
            assert 'TestCSharpLocaleNativeInterpretedCompiled/'+locale in passes
        if a.corpus:
            recipes=json.loads((root/'lower/testdata/powershell-locale/recipes.json').read_text())
            assert len(recipes)==16
            for case in recipes:
                assert 'TestPowerShellLocaleRecipeCorpus/'+case in passes, case
    print('PASS',package, len(passes)-1,'named tests/subtests, zero skips/failures')
(evidence/'PASS').write_text('Native/interpreted/compiled locale gate passed; manager acceptance remains separate.\n')
