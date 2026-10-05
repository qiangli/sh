#!/usr/bin/env python3
"""Run an opt-in collector probe in a fresh Linux cgroup v2 (requires delegation).

Example (from interp, after go test -c -o /tmp/collector.test ./interp):
  ../scripts/collector-pressure.py --output /tmp/pressure -- /tmp/collector.test \
    -test.v -test.run '^TestS376MemoryPressure$' -test.timeout=30s

The output prefix must be new. Preserve all three artifacts, including failures.
The runtime budget is soft; memory.max is the independent kernel hard limit.
"""
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--memory-mib", type=int, default=512)
    parser.add_argument("--timeout", type=float, default=30)
    parser.add_argument("--cgroup-parent", type=Path, default=Path("/sys/fs/cgroup"))
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command or args.memory_mib <= 0 or args.timeout <= 0:
        parser.error("command, positive memory allowance and timeout required")
    paths = [Path(str(args.output) + suffix) for suffix in (".log", ".time", ".json")]
    if any(path.exists() for path in paths):
        parser.error("output already exists; keep failures and use a fresh prefix")
    group = args.cgroup_parent / ("s376-collector-" + str(os.getpid()))
    group.mkdir()
    process = None
    result = {"command": command, "memory_max": args.memory_mib << 20,
              "timeout_seconds": args.timeout, "cwd": os.getcwd()}
    try:
        (group / "memory.max").write_text(str(result["memory_max"]))
        (group / "memory.swap.max").write_text("0")
        env = dict(os.environ, S376_MEMORY_PRESSURE="1")
        for key in ("GOGC", "GOMEMLIMIT"):
            env.pop(key, None)
        # Keep time outside the memory cgroup so an OOM still produces RSS data.
        wrapped = ["/usr/bin/time", "-v", "-o", str(paths[1].absolute()),
                   "/bin/sh", "-c", 'echo $$ > "$1/cgroup.procs" || exit; shift; exec "$@"',
                   "collector", str(group), *command]
        started = time.monotonic()
        with paths[0].open("x") as output:
            process = subprocess.Popen(wrapped, stdout=output, stderr=subprocess.STDOUT,
                                       env=env, start_new_session=True)
            try:
                result["exit_code"] = process.wait(timeout=args.timeout)
                result["time_limit"] = False
            except subprocess.TimeoutExpired:
                result["time_limit"] = True
                (group / "cgroup.kill").write_text("1")
                try:
                    process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                result["exit_code"] = process.returncode
        result["elapsed_seconds"] = time.monotonic() - started
        for name in ("memory.max", "memory.swap.max", "memory.peak", "memory.events"):
            result[name] = (group / name).read_text().strip()
        paths[2].write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps(result))
        return 124 if result["time_limit"] else result["exit_code"]
    finally:
        if process is not None and process.poll() is None:
            (group / "cgroup.kill").write_text("1")
            process.wait()
        group.rmdir()


if __name__ == "__main__":
    raise SystemExit(main())
