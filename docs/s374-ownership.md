# Native transport ownership

A transport root is a lease, not the native object's lifetime. Native handles
can drop their strong table entry when their last interpreter owner disappears:
other native objects retain ordinary Go references independently. Copies,
interfaces and task snapshots must share an owner token; requests must keep that
token alive through their reply. Each outgoing occurrence receives a unique lease
so a delayed release cannot revoke a later delivery of the same pointer handle.
Release messages run on the existing authenticated connection. The worker removes
its value, pointer-identity index and reflected receiver metadata together only
when all leases end. Unsent structural snapshots also need local ownership: they
must not create permanent table entries merely by encoding an aggregate.

Pointer origins require a different, two-sided protocol. An origin denotes two
physical copies of one logical object; native callbacks can need the interpreter
copy even after its source variable dies. Weakening both copies loses live native
owners; rooting each copy on behalf of the other leaks cycles. A safe implementation
needs explicit native-retention edges (including globals, goroutines, callbacks,
interior pointers and reflected values), or an epoch handshake that temporarily
removes bridge-only roots while pinning in-flight work and tracing actual owners.
The current reflection bridge does not expose all such edges. A finalizer attached
to the pointer wrapper is insufficient: another wrapper may address the same cell.

Closure strings similarly do not carry a GC-visible owner. Replacing their registry
with weak pointers requires ownership in every value representation, including raw
strings in aggregates and task snapshots. Scanning only current runner bindings
misses active frames, deferred calls and native retained callbacks.

Implement and test the handle lease boundary independently. Do not make pointer
or closure tables weak, capped or call-scoped until those missing edges have a
verified ownership representation. Tests must distinguish handle leases from
origin/callback ownership; a bounded handle table alone is not complete delivery.

## Implemented lease protocol

`encodeHandle` acquires a temporary worker owner. Its cleanup reclaims unsent
encodings, including structural snapshots used only for comparison. Before a
response or callback is serialized, every nested handle receives a new lease.
The interpreter adopts those leases at decoding (including mailbox callbacks)
and copies a shared owner pointer with the bridge value. Cleanup state has no
back-reference to the session, so cached templates cannot root a dead session.

Cleanups queue release IDs; the next real request drains them under the socket
write lock. Requests keep their owners alive through their replies. The worker
processes release IDs before dispatching that request, ignores duplicate IDs,
and deletes all per-handle indexes when its last temporary/exported owner ends.
Optional interpreter handle facts are discarded along with released leases.
Callback result values are pinned on the interpreter until the worker explicitly
acknowledges acquiring temporary owners, including on the shared-memory mailbox
path. Native objects are never destroyed by this protocol: ordinary native
references continue to own them independently.

The worker's live table is bounded by current owners plus garbage awaiting each
process's GC and queued releases awaiting the next request. This is eventual
reclamation, not a fixed-size eviction policy or synchronous destruction on
assignment. Origins, pinned slice regions and original closure registrations
are not reclaimed by this change.

Pointer-valued native fields can change after `receiver_field` exposes their
storage. The pointer index therefore records its original key separately from
the mutable reflect.Value; retirement removes that exact key and validates the
mapped handle before reuse. A changed field cannot redirect an independently
retained native pointer through a stale index entry.
