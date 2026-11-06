#!/usr/bin/env python3
"""Bound cold C++ SDK preparation separately from the 30s behavior tests."""
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import time

out = Path(sys.argv[1]).resolve()
out.mkdir()
compiler = shutil.which("clang++")
assert compiler, "clang++ wrapper missing"
# Exercise the same standard library as the stream tests, with no diagnostic
# suppression, sanitizer override, or reuse of a foreign corpus cache.
source = out / "probe.cpp"
source.write_text('''#include <fstream>
#include <iostream>
#include <sstream>
int main() {
  std::ifstream missing("missing-input");
  std::istringstream empty("");
  std::cout << empty.rdbuf();
  return std::cout.fail() && !missing.is_open() ? 0 : 1;
}
''')
deadline = time.monotonic() + 90
records = []
try:
    for label, flags in [("native", ["-std=c++17"]),
                         ("worker", ["-std=c++20", "-O0"])]:
        binary = out / label
        commands = [[compiler, *flags, str(source), "-o", str(binary)],
                    [str(binary)]]
        for phase, argv in zip(["compile", "run"], commands):
            record = {"label": label, "phase": phase, "argv": argv}
            records.append(record)
            start = time.monotonic()
            with (out / f"{label}.{phase}.stdout").open("wb") as stdout, \
                 (out / f"{label}.{phase}.stderr").open("wb") as stderr:
                try:
                    process = subprocess.Popen(argv, cwd=out, stdout=stdout, stderr=stderr,
                                               start_new_session=True)
                    record["rc"] = process.wait(timeout=max(0.001, deadline-start))
                except subprocess.TimeoutExpired:
                    record["timeout"] = True
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                    raise
                finally:
                    record["seconds"] = time.monotonic()-start
            assert record["rc"] == 0, record
            if phase == "run":
                assert not (out / f"{label}.{phase}.stdout").read_bytes(), record
                assert not (out / f"{label}.{phase}.stderr").read_bytes(), record
finally:
    (out / "commands.json").write_text(json.dumps(records, indent=2) + "\n")
print("PASS bounded native/worker C++ provider preparation (not product acceptance)")
