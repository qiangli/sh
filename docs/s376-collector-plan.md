# Collector budget and repeated bridge encoding

The implementation keeps the interpreter's high-throughput GOGC target while
adding a host-memory-aware Go runtime soft limit. Use one quarter of physical
memory, further constrained by Linux cgroup v1/v2 memory limits; use 512 MiB
when host capacity is unavailable. Preserve explicit GOMEMLIMIT and stricter
embedding limits. Nest runs under the existing process-wide lock and restore
both runtime settings when the final run releases its lease.

This budget leaves room for the dependency worker and other host memory. It
is not a hard RSS ceiling and cannot reduce memory proportional to live program
data. Linux discovery covers the conventional /sys/fs/cgroup controller
mounts and their process ancestry; nonstandard controller mounts fall back to
physical memory. Discovery happens at the start of a group of overlapping runs,
not continuously when an administrator changes a container limit.

For repeated collection snapshots, size the element buffer once and iterate
struct declaration fields directly. Do not cache encoded values across calls:
deferred arguments, mutations, capacity tails, lazy reflection and pointer
ownership require fresh snapshots. This reduces temporary allocations and
copies; it does not remove all repeated graph traversals.

Validation uses a bounded slice-of-struct encoding/preparation benchmark, a
mutation-between-capture-and-send test, pacing nesting/restoration/host override
tests, Linux controller parsing cases, and existing ownership, resident-sync,
callback and Tour image checks. Compare allocation bytes/count, post-GC live
heap, peak RSS and elapsed time on the same candidate. The memory budget needs
separate Linux stress validation; a small allocation benchmark cannot establish
an RSS ceiling. Whole-package runs remain stress evidence, not interpreted
acceptance roots. Keep time-limit outcomes and live-data work separate.

## Reproducible Linux pressure probe

Build a full-tag interp test binary for each exact source revision. Add the
same `gosource_s376_encoding_test.go` and `gosource_s376_pressure_test.go` to
the baseline; keep its production sources unchanged. Run serially, with no
concurrent tutorial or corpus workload:

```sh
go test -tags full -c -o /tmp/collector.test ./interp
cd interp
python3 ../scripts/collector-pressure.py --output /tmp/collector-pressure \
  --memory-mib 512 --timeout 30 -- /tmp/collector.test \
  -test.v -test.run '^TestS376MemoryPressure$' -test.timeout=30s
```

The opt-in test retains 96 MiB and allocates 512 transient 8 MiB buffers,
touching every page. It prints the actual runtime memory limit, intermediate
heap/allocation counts and elapsed time, then post-GC live heap and allocations
per iteration. It deliberately models garbage collection, not bridge encoding
or interpreted live-value representation. In this 512 MiB cgroup the automatic
candidate budget should be 128 MiB, with 96 MiB still live. A lower resident
peak cannot count as a fix for issue80188.

The Python runner needs Linux cgroup v2 delegation (root works on the gate
host), GNU time and Python 3. It creates a fresh child cgroup, disables swap,
clears GOGC/GOMEMLIMIT overrides, and records the hard allowance, cgroup peak,
OOM events, process peak RSS, elapsed time and exit status. The 30-second
watchdog kills the cgroup on expiry and records a time-limit result. It refuses
to overwrite existing output. Preserve `.log`, `.time` and `.json` for every
run, including OOMs; incomplete iterations are not completed-call metrics.
