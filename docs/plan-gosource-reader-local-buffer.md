# Bounded local buffer execution for original Reader callbacks

Sprint: #118; Story: #54; Story-ID: c3a60493cde9

The unchanged Tour reader validator consumes 1 MiB. Its original `Read` loop
still runs through the interpreter. A strict positioned-AST proof permits one
range over its byte parameter, with one literal byte assignment to the range
index, followed by `return len(parameter), nil`. The proof rejects aliases,
receiver/global effects, named results, arbitrary calls, early returns, deferred
work and concurrency. Runtime binding also rejects shadowed `len`, `nil`,
`byte` and `uint8`. It matches syntax and bindings, never a source path or hash.

For this shape only, the dependency stub sends a full-capacity byte snapshot
alongside the authenticated actual buffer handle. The host independently
rechecks the proof, constructs ordinary interpreter slice storage with matching
length/capacity metadata, and invokes the original method. The returned bytes
are validated and copied back to the exact native backing buffer before the
waiting synchronous consumer resumes. The original body cannot retain or expose
this temporary storage. No original statement is emitted into the helper.

General Reader bodies retain the native-handle path, including retained aliases,
partial errors, panic, nested dependency reads and rot13. This is not a general
escape analysis, asynchronous callback grant, or generic slice-identity claim.
Cancellation prevents a successful local-buffer reply; Reset invalidates the
native session as before.

Measured against unchanged originals, enforcing the existing Tour 60-second
step deadline, the 1 MiB interpreted reader completed in 2.654 seconds (previous
native-handle run: 75.177 seconds). Native Go and the source-free compiled
artifact matched both output streams exactly. Rot13 completed interpreted in
1.282 seconds and also matched both other modes. Raw bytes, source and artifact
hashes and exact commands are retained under
`~/.local/state/bashy/sprint118-evidence/reader-fast-019/manifest.json`.

The focused race gate includes the full original validator, rot13, capacity and
high-byte writeback, shadowed-len alias retention, overlapping escaped buffers,
partial errors, panic-visible writes, rejected retained consumers, cancellation
and stale-session/Reset controls. It passed in 47.064 seconds. Existing cancellation/Reset checks
exercise the general native-handle callback path. Large single-call snapshots
can still spend substantial time in the existing interpreter object preflight;
this slice does not change that validation boundary.
