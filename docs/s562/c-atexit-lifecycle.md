# Sprint 562: C one-shot worker completion

Story #3088 / `5d9301d113ca`; base `68816971`; manager `codex-gpt6-sol`.

## Diagnosis and bounded design

`Module.callNativeArtifact` starts a fresh native executable for every call.
The generated C `main` calls the exported function, emits its return value,
and returns zero. libc then executes registered `atexit` handlers in reverse
registration order and flushes streams. Go's `cmd.Run` already waits for this
whole lifetime and captures stdout/stderr. There is no premature wait or JSON
serialization here: the frame is `RS BASHPP TAB status TAB kind TAB hex LF`.
The host incorrectly treats the entire suffix after the marker as hex, so
exit-handler stdout invalidates a perfectly good return frame.

The fix is restricted to the shared C/C++ host decoder in `polyglot/c.go`: consume one
newline-terminated frame and concatenate the stdout bytes before and after
it. Preserve tabs, NULs and missing final newlines in callback output. On
Windows, tolerate the frame's stdio CRLF without trimming user output. Check
process failure after extracting the frame, so a callback's nonzero exit
cannot be hidden by an earlier successful return value.

Multiple markers are rejected as ambiguous, including callback output imitating
a frame; neither first nor last marker wins. Invalid fields, hex, status and
unterminated frames are rejected.

This keeps source, compiler, exports, function ABI, generated worker, libc
callback registration/order, and stderr intact. C++ uses the same bounded extraction.
The worker-generation/module-assets changes owned by `callback_triage`
(`4afecc`) are outside this patch. The existing stdout marker protocol is
retained; this is not a general protocol-channel redesign.

A function returning 7 produces value 7 with worker status zero. `exit(0)`
inside the function runs callbacks but does not produce a return value, so
it remains a missing-frame error. `exit(23)` retains an `exec.ExitError`
with code 23. A callback calling `exit(29)` after the function returned
retains code 29 and the captured output. `_Exit(31)` bypasses callbacks.
No synthetic return value is inferred from process exit.

## Immutable fixture and evidence

`internal/testdata/c-atexit/L53.bsh` and `.expected` are byte-for-byte copies
from the reviewed corpus at `c-runtime-library/atexit/`.
The generated fixture records MicrosoftDocs/cpp-docs revision
`281a0a0eae55fe97f5decd244db18ed5096d4687`, MIT code, upstream line 53.
The expected file is 54 bytes: seven-byte `# rc=0\n` metadata followed by
47 bytes of stdout:

```
This is executed first.
This is executed next.
```

The focused native test extracts the unchanged C fence and compiles a real
clang C17 driver, then compares status, stdout and stderr with hardcoded
expectations and the worker. Each worker case is called twice to check that
one-shot callback state does not leak between calls. The lower tests execute
both the interpreter and a compiled Go artifact, checking the golden and
return/exit/callback-failure cases. Existing C/C++ ABI and private-declaration
regressions are included in the gate.

Local macOS arm64 evidence: baseline overlay reproduces exactly
`invalid C worker result payload`, returning only the first line. Candidate
native lifecycle and interpreted/compiled tests pass. The script validates
individual test PASS events, rejects skips/failures and checks package PASS;
command exit zero alone is insufficient. Detailed logs remain in private
manager evidence. No full foreign-development suite ran.

## Manager-only Linux gate

The manager reviews the immutable bundle and checksum manifest on the Linux
validation host. Deployment coordinates and exact operational commands remain
in private evidence. The staged driver accepts explicit paths:

```sh
bash docs/s562/run-c-atexit-remote.sh "$STAGE_DIR" "$ZIG_BIN" "$GO_BIN" "$CORPUS_ROOT"
```

`STAGE_DIR` contains `worker.bundle` and `SHA256SUMS`; it must have no previous
source, tool-wrapper, cache, temporary or results directories. `ZIG_BIN` and
`GO_BIN` are absolute executable paths to the reviewed Zig 0.16.0 and Go 1.27.2
tools. Build caches stay within the stage. From an existing candidate checkout,
run the focused gate directly with a fresh results path:

```sh
bash docs/s562/verify-c-atexit.sh "$NEW_RESULTS" "$CORPUS_ROOT"
```

The second argument must directly contain `c-runtime-library/atexit/L53.bsh`
and its golden. If the reviewed baseline nests `c-by-example`, pass that
subdirectory. The script only reads the baseline and refuses an existing
results directory. It records candidate commit, worktree patch, compiler
and Go identities, reproduces baseline red with an overlay, then runs only
the focused polyglot and lower gates. Linux results remain manager-owned;
this delivery makes no C197-wide pass-count claim.

## Review follow-up evidence

The analogous C++ probe uses an atexit callback and function-local static
object. The real native oracle emits `body\ncallback\ndestructor\n`, with
`stderr\n` on stderr. Baseline `68816971` returns only `body\n` then reports
`invalid C++ worker result payload`. The shared decoder fix preserves all
three lines. Native exit(0) and exit(23) retain the same no-frame and native
failure distinctions as C. Native and worker calls are compared twice;
interpreted and compiled C++ coverage checks both output streams and value 7.

Local follow-up validation passed every required native, interpreter and
compiled test with no skips; the updated gate requires both baseline reds.
The manager subsequently reported the Linux lifecycle gate PASS in 87.549s
for product commit `353fb03b`. Logs and deployment details remain in private
evidence; the documentation correction does not rerun unchanged product gates.
Malformed/multiple-frame decoder controls run for both languages. This follow-up
does not touch worker generation or module assets owned by callback_triage.
