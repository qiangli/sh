# Private dependency-helper scratch

Sprint: #118  
Story: #56  
Story-ID: 3ef468f4e831

Generated dependency helper inputs, overlay, and executable now live in a private
0700 directory under the caller's TMPDIR (or the platform temporary directory).
An authenticated dependency session uses its original process environment for
this resource setting. Original source files are never rewritten or copied into
the helper. An explicit TMPDIR inside the source context is rejected rather than
mutating that tree; an unavailable temporary directory also fails closed.

A Go overlay supplies a virtual named-file importer inside the original module
visibility tree. Both the existing context and temporary root are canonicalized:
on macOS Go canonicalizes its working directory, and an overlay through a
symlink alias otherwise breaks real internal-package imports. The virtual
subdirectory does not exist physically and cannot mask an existing source path.
Build and runtime working directories, module/workspace/vendor/GOPATH resolution,
original environment, argv, and dependency init behavior remain as before.

The persistent helper removes its build scratch after the dependency process
has authenticated, before executing original interpreter statements. This also
avoids leaving source, binary, or overlay effects when the host subsequently
receives SIGTERM. Normal close/Reset/cancellation retry idempotent cleanup.

Transport EOF or explicit close now ends the dependency helper process after
signaling channel cancellation. It does not wait for pending native calls: a
TCP listener Accept may otherwise block forever, orphan the helper after host
SIGTERM, and keep inherited output pipes open. Returning from helper main
terminates those dependency goroutines, matching process shutdown; original
program bodies and their defers remain interpreter-owned.
Platforms that prohibit unlinking running executables can require that later
cleanup; abrupt-termination cleanup is demonstrated here on Unix, not asserted
for Windows. The legacy call/value bridge cleans its private scratch after its
ordinary bounded invocation.

Regression coverage uses actual Go builds and artifacts: a symlinked module and
internal import, workspace use/replace, vendor, GOPATH, concurrent package
consumers, input permissions, failure cleanup, dependency initialization, and
native-channel cancellation/Reset. The retained upstream TCP source has SHA256
`07d7f4491bd29ae68fa991da93a406d9746cc3dbd91afb97c36b8aa8b6cfec6c`.
Its standalone process test compares oracle and interpreter ACK, raw streams,
SIGTERM143, listener release, and complete source/runtime trees while alive and
afterward. It takes `/tmp/s118-tcp-8090-manager-lease`; other corpus processes
using the literal upstream port must be scheduled separately. The compiled TCP
path is outside this change.

Focused validation on the isolated `7422daf2` successor:

- Real import/module/private-scratch/cancellation controls under `-race`: PASS,
  13.966 seconds.
- Session isolation, public Subshell ownership, and cancellation under `-race`:
  PASS, 6.919 seconds.
- Read-only original module plus symlink/internal import and invalid TMPDIR:
  PASS, 0.733 seconds.

Initial fixed-port attempts are retained as failures: one overlapped the
independent corpus HTTP responder, and another exposed inherited test-binary
re-exec plumbing. The final test uses a standalone real Runner executable,
detects child failure before readiness, and preserves both raw streams without
filtering. These failed attempts are not acceptance evidence.

The exact unchanged TCP oracle/interpreter check subsequently passed in 5.108
seconds, with empty raw stdout/stderr, matching ACK, status143, listener release,
and unchanged trees. A preceding run exposed the blocked-Accept orphan and was
manually cleaned up; that attempt is explicitly invalid evidence.

Final combined gate after the EOF correction: PASS, 18.930 seconds, using
`GOMAXPROCS=2 go test -race -p=2 ./interp` with the import/private-scratch,
workspace/vendor/GOPATH, module initialization, session/Subshell/cancellation,
native-channel Reset, and exact TCP tests selected. The exact TCP check passed
again in that gate (2.35 seconds) without external cleanup or stream filtering.
The standalone Runner driver is a normal build; the surrounding in-process
lifecycle controls run under the race detector.
