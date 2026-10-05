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
