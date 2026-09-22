# Opening a Windows FIFO (Sprint 246, S246.8 — reader half)

`mkfifo` on Windows cannot create a pipe on the filesystem, so it writes a
**marker file** naming a `\\.\pipe\` pipe instead. The format is frozen in
the coreutils repo's `docs/windows-fifo.md` (format v1); the applet there is
the writer, and `sh/interp` — this change — is the opener. **The bytes are a
wire contract: changing them is a break that needs a version bump in the
magic line.** Read that document before touching any of this.

What it unblocks: `builtins` line 123 (`four - OK`, from
`tests/source6.sub:44-49`) and the fd-9 tail of `read` (`tests/read2.sub:60-64`).

## Where it lives

| file | build | what |
| --- | --- | --- |
| `interp/fifo_marker.go` | all | the v1 format: grammar, leaf validation, marker bytes, pipe names, the FIFO `FileInfo` |
| `interp/fifo_marker_windows.go` | windows | detection, the three open modes, the read-write loopback, the stat hook, marker creation |
| `interp/fifo_marker_other.go` | !windows | the stat hook as the identity |
| `interp/os_windows.go` | windows | `openPath` calls detection first; `mkfifo` writes a marker |
| `interp/handler.go` | all | `DefaultStatHandler` runs results through the stat hook |

The format layer is deliberately platform-neutral so that the part which is
a contract with another repo is provable on any host — `fifo_marker_test.go`
runs on macOS and Linux and pins the golden 57 bytes plus every reject.
Everything that needs a real named pipe is in `fifo_marker_windows_test.go`.

## Decisions worth knowing

**Detection is all three rules or nothing.** Regular file, ≤128 bytes,
`FILE_ATTRIBUTE_SYSTEM`, content matching the grammar exactly. A file that
satisfies some of them is the ordinary file it is. Leaf strictness is a
security boundary — a looser line 2 would let a crafted marker aim an open
at `\\.\pipe\lsass` or a squatted service pipe — so it rejects and never
repairs. `GetFileAttributesEx` answers the attribute and size rules in one
call, before anything is opened, which keeps ordinary opens free and keeps
the marker read (the only step that can deny an otherwise-fine open) off
every path but a real candidate's.

**The one POSIX divergence the document records** is that a marker must be
read even for a write-only open, so a FIFO whose mode denies read denies
that class every open. Reporting `EACCES` there beats writing over a
marker's bytes.

**`O_RDWR` is a loopback, not a pair.** The document says the fd is the
FIFO's server instance plus this shell's own client of it. Those are two
handles, but a `<>` redirection is bound to a numbered fd as an `*os.File`
and nothing else (`interp`'s fd table is `map[int]*os.File`, and
`stdinFile` copies anything else through an `os.Pipe` — which would silently
drop the write half). A single handle cannot do it either: on a named pipe
a write to the server end arrives at the *client*, and every FIFO instance
is fixed at `PIPE_ACCESS_INBOUND` so a duplex instance would not interop
with the plain readers other opens create. So `fifoLoopback` makes a private
duplex pipe, hands its client to the shell as the fd, and splices its server
onto the FIFO's two handles. Closes cascade in one direction, so nothing is
reclaimed by hand. Recorded deviation: bytes in flight can sit in three
kernel buffers rather than one, so a script that writes without ever reading
blocks later than on a Linux FIFO — both block, only the count differs.

**Unlink needs nothing.** Handles are the pipe's, never the marker's: the
marker is opened only to be read, and closed before the rendezvous starts.
`rm -f a.pipe` therefore works while `exec 9<> a.pipe` stays live, which is
what read2.sub does before its 2000 iterations.

**It is not `procsubst_windows.go`.** That serves one pipe for one command,
holds the server end itself, and answers a second open with end-of-stream. A
FIFO's name is on disk and outlives the shell, every open is a fresh
rendezvous, and the marker file is the registry a stat is answered from.
What is shared is the mechanics of a blocking `ConnectNamedPipe` that has to
be callable off — `connectInstance` and the client-connect nudge.

## Verified

`go build ./...`, `go vet ./...`, `go test ./interp/` on darwin;
`GOOS=windows go vet ./...`, `GOOS=windows go test -c ./interp/`, and
`GOOS=windows/plan9/js go build ./...`. The Windows behaviour tests cannot
run off Windows — the conductor measures them on CI.
