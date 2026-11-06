#!/usr/bin/env python3
"""Import the native fixture owner's layout into the manifest-relative contract.

Never changes the source fixture tree. Graph, source, observer, key and diagnostic
bytes remain exact; only the product configuration points at the relocated graph.
This is a packaging adapter, not upstream authentication or native proof.
"""
import argparse
import hashlib
import json
import pathlib
import shutil

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', required=True)
p.add_argument('--output', required=True)
a = p.parse_args()
source = pathlib.Path(a.source).resolve()
output = pathlib.Path(a.output).resolve()
assert source != output and source not in output.parents, 'output must be separate'
assert not output.exists(), 'output must be fresh'
receipt = {}
for name in ('protected', 'cs1705'):
    root = source / name
    config = json.loads((root / 'bashpp.json').read_bytes())
    assert config == {'language': 'csharp', 'runtime': 'pwsh', 'csharp_assemblies': 'assets/graph.json'}
    graph_bytes = (root / 'assets/graph.json').read_bytes()
    graph = json.loads(graph_bytes)
    assert graph['schema'] == 'csharp-assembly-graph-v1'
    for asset in graph['sources'] + graph.get('keys', []):
        path = pathlib.PurePosixPath(asset['path'])
        assert not path.is_absolute() and '..' not in path.parts and path.parts[0] == 'assets'
        data = (root / path).read_bytes()
        assert hashlib.sha256(data).hexdigest() == asset['sha256'], asset['id']
    files = {}
    for path in sorted(root.rglob('*')):
        assert not path.is_symlink(), 'symlinks are not fixture inputs'
        if path.is_file():
            files[path.relative_to(root).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    receipt[name] = {'input_sha256': files, 'relocation': {'from': 'assets/graph.json', 'to': 'graph.json'}}
# Validate both complete inputs before creating any output.
output.mkdir(parents=True)
for name in receipt:
    shutil.copytree(source / name, output / name)
    (output / name / 'assets/graph.json').rename(output / name / 'graph.json')
    config = {'language': 'csharp', 'runtime': 'pwsh', 'csharp_assemblies': 'graph.json'}
    (output / name / 'bashpp.json').write_text(json.dumps(config) + '\n')
    receipt[name]['output_sha256'] = {
        path.relative_to(output / name).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
        for path in sorted((output / name).rglob('*')) if path.is_file()
    }
(output / 'packaging-receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
print('Prepared exact-byte assets with manifest-relative layout; receipt: packaging-receipt.json')
