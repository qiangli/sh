#!/usr/bin/env python3
"""Verify the proposal's discovery receipt, not native or product acceptance."""
import argparse
import hashlib
import pathlib
import re
import subprocess
import tarfile


def require(ok, message):
    if not ok:
        raise SystemExit("FAIL: " + message)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--upstream-archive", type=pathlib.Path, required=True)
    parser.add_argument("--combined-runtime", type=pathlib.Path, required=True)
    args = parser.parse_args()
    proposal = pathlib.Path(__file__).with_name("csharp-assembly-assets-proposal.md").read_text()
    files = re.findall(r"\| (language-reference/[^ |]+\.md) \| `([0-9a-f]{64})`", proposal)
    fences = re.findall(r"\| (protected-internal|cs1705) L(\d+) \| `([0-9a-f]{64})`", proposal)
    require(len(files) == 2 and len(fences) == 6, "receipt denominator must be two files and six fences")
    checked = 0
    with tarfile.open(args.upstream_archive, "r:gz") as archive:
        for relative, digest in files:
            name = "dotnet-docs/docs/csharp/" + relative
            matches = [m for m in archive.getmembers() if m.name == name]
            require(len(matches) == 1 and matches[0].isfile(), "source must be one regular archive member")
            require(matches[0].size <= 1024 * 1024, "unexpected source size")
            data = archive.extractfile(matches[0]).read()
            require(hashlib.sha256(data).hexdigest() == digest, "source hash " + relative)
            lines = data.splitlines(keepends=True)
            for stem, start, expected in fences:
                if pathlib.PurePosixPath(relative).stem != stem:
                    continue
                index = int(start)
                require(lines[index - 1].strip() == b"```csharp", "opening fence " + start)
                end = next(i for i in range(index, len(lines)) if lines[i].strip() == b"```")
                body = b"".join(lines[index:end])
                require(hashlib.sha256(body).hexdigest() == expected, "fence hash " + stem + start)
                checked += 1
    require(checked == 6, "all six source fences checked")
    root = args.combined_runtime
    def git(*argv):
        return subprocess.check_output(["git", "-C", str(root), *argv], text=True).strip()
    require(git("rev-parse", "HEAD") == "f1a41ac0f3dc21a441042bec8bd95cec4a4dbcc9", "reviewed baseline identity")
    require(not git("status", "--porcelain", "--untracked-files=all"), "reviewed baseline clean")
    source = (root / "polyglot/csharp.go").read_text()
    compile_body = source.split("function Compile-Fence(", 1)[1].split("function MapCSharpType", 1)[0]
    require("Add-Type -TypeDefinition $normalized" in compile_body, "baseline compiler path")
    require("-ReferencedAssemblies" not in compile_body, "baseline has no explicit reference list")
    require("bashpp-csharp-v2" in source, "baseline cache namespace")
    print("PASS: 2 file hashes, 6 fence hashes, exact clean combined baseline and compiler gap")
    print("NOT RUN: upstream commit authentication; Linux/Windows native and product graph acceptance")


if __name__ == "__main__":
    main()
