#!/usr/bin/env python3
"""Bounded manager gate for native Node, interpreted, and compiled locale parity."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--bashy", required=True, type=Path,
                    help="candidate bashy executable with managed Node/TypeScript provisioning")
parser.add_argument("--go", required=True, type=Path, help="authenticated Go SDK executable")
parser.add_argument("--sh-root", required=True, type=Path,
                    help="candidate mvdan.cc/sh/v3 checkout")
parser.add_argument("--evidence", required=True, type=Path,
                    help="new directory for bounded stage records")
parser.add_argument("--build-cache-seed", type=Path,
                    help="optional reviewed cache copied privately before stage deadlines")
args = parser.parse_args()

bashy = args.bashy.resolve(strict=True)
go = args.go.resolve(strict=True)
sh_root = args.sh_root.resolve(strict=True)
if not (sh_root / "go.mod").is_file():
    raise SystemExit("FAIL --sh-root has no go.mod")
evidence = args.evidence.resolve()
evidence.mkdir(parents=True, exist_ok=False)
fixture_root = Path(__file__).resolve().parent


def safe_base():
    env = {key: os.environ[key] for key in ("HOME", "TMPDIR") if key in os.environ}
    env.update(PATH="/nonexistent", BASHY_HINTS="off", TZ="UTC")
    return env


def build_env(cache):
    env = {key: os.environ[key] for key in
           ("HOME", "TMPDIR", "PATH", "GOMODCACHE", "GOPATH") if key in os.environ}
    env.update(GOCACHE=str(cache), GOWORK="off", GOTOOLCHAIN="local", GOPROXY="off",
               GOSUMDB="off", GOENV="off", GOTELEMETRY="off")
    return env


records = []


def invoke(case, mode, argv, cwd, env, timeout):
    try:
        done = subprocess.run([str(arg) for arg in argv], cwd=cwd, env=env,
                              capture_output=True, text=True, timeout=timeout)
        record = {"case": case, "mode": mode, "argv": [str(arg) for arg in argv],
                  "cwd": str(cwd), "exit": done.returncode,
                  "stdout": done.stdout, "stderr": done.stderr}
    except subprocess.TimeoutExpired as error:
        record = {"case": case, "mode": mode, "argv": [str(arg) for arg in argv],
                  "cwd": str(cwd), "timeout_seconds": timeout,
                  "stdout": error.stdout or "", "stderr": error.stderr or ""}
    records.append(record)
    (evidence / f"{len(records):02d}-{case}-{mode}.json").write_text(
        json.dumps(record, indent=2, sort_keys=True) + "\n")
    if "exit" not in record:
        raise SystemExit(f"FAIL {case}/{mode}: timeout after {timeout}s")
    if record["exit"] != 0 or record["stderr"]:
        raise SystemExit(f"FAIL {case}/{mode}: exit={record['exit']} stderr={record['stderr']!r}")
    return record["stdout"]


cases = {
    "absent": {},
    "empty": {"LANG": "", "LC_ALL": "", "LC_CTYPE": ""},
    "lang": {"LANG": "tr_TR.UTF-8"},
    "lc_all": {"LC_ALL": "tr_TR.UTF-8"},
    "lc_ctype": {"LC_CTYPE": "tr_TR.UTF-8"},
}

with tempfile.TemporaryDirectory(prefix="s562-ts-locale-") as temporary:
    root = Path(temporary)
    if args.build_cache_seed is not None:
        shutil.copytree(args.build_cache_seed.resolve(strict=True), root / "gocache")
    source = root / "locale.bsh"
    oracle = root / "locale.mjs"
    shutil.copyfile(fixture_root / "typescript-locale-repro.bsh", source)
    shutil.copyfile(fixture_root / "typescript-locale-oracle.mjs", oracle)
    for name, explicit in cases.items():
        case_dir = root / name
        case_dir.mkdir()
        case_source = case_dir / source.name
        case_oracle = case_dir / oracle.name
        shutil.copyfile(source, case_source)
        shutil.copyfile(oracle, case_oracle)
        env = safe_base()
        env.update(explicit)
        native = invoke(name, "native-node", [bashy, "node", case_oracle.name],
                        case_dir, env, 20)
        interpreted = invoke(name, "interpreted", [bashy, "--bashsharp", case_source.name],
                             case_dir, env, 20)
        if interpreted != native:
            raise SystemExit(f"FAIL {name}: interpreted output differs from native Node")

        # Lowering resolves the foreign runtime too. Establish its exact local
        # graph before transpilation, not only before compiling generated Go.
        modfile = case_dir / "go.mod"
        modfile.write_text(
            "module s562localefixture\n\ngo 1.27\n\n"
            "require mvdan.cc/sh/v3 v3.0.0\n"
            f"replace mvdan.cc/sh/v3 => {json.dumps(str(sh_root))}\n")
        shutil.copyfile(sh_root / "go.sum", case_dir / "go.sum")
        lower_env = env.copy()
        compiler_env = build_env(root / "gocache")
        for key in ("GOCACHE", "GOPATH", "GOMODCACHE", "GOWORK", "GOTOOLCHAIN",
                    "GOPROXY", "GOSUMDB", "GOENV", "GOTELEMETRY"):
            if key in compiler_env:
                lower_env[key] = compiler_env[key]
        lower_env["GOFLAGS"] = "-mod=mod " + json.dumps("-modfile=" + str(modfile))
        generated = case_dir / "generated.go"
        invoke(name, "transpile", [bashy, "transpile", "--bashsharp", case_source.name,
                                   "-o", generated], case_dir, lower_env, 30)
        artifact = case_dir / ("locale.exe" if os.name == "nt" else "locale")
        invoke(name, "build", [go, "build", "-mod=mod", "-o", artifact, generated],
               case_dir, build_env(root / "gocache"), 120)
        compiled = invoke(name, "compiled", [artifact], case_dir, env, 20)
        if compiled != native:
            raise SystemExit(f"FAIL {name}: compiled output differs from native Node")

summary = {"result": "PASS", "cases": list(cases), "stages": len(records),
           "bashy": str(bashy), "go": str(go), "sh_root": str(sh_root)}
(evidence / "summary.json").write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n")
print("PASS native Node == interpreted == compiled for explicit TS/JS locale fixtures")
