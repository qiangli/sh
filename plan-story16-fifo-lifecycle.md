# Story 16: task-group FIFO lifecycle

Base: b009bbd8, retaining current selected-Go and gate changes. Replace the
task-only blocking reopen with acquired-inode registered-peer rendezvous.
Native reader descriptors remain open through the handshake; pending writers
hold an unregistered reader probe. No blocking open goroutine or cancellation
guard is needed. External/unregistered peers do not satisfy the task-group
handshake, and cancellation reports that explicit unsupported scope.

Retire entries when persistent virtual descriptors close or are replaced,
retaining true aliases and exact temporary restoration bindings. Snapshot
native descriptors under writer wrappers too; each task owns its duplicate.
Task and File cleanup force-close owned entries. Classic/POSIX open behavior
remains untouched. FIFO registrations do not survive their File task group;
writer acquisition requires read permission for its inode-pinning probe.

Verify deterministic reader/writer-first rendezvous, cancellation, replacement,
external-peer refusal, virtual fd lifetime, escaping aliases, wrapped writer
snapshots and File cleanup. Run focused memory-bounded race stress only in
coordination with the main reviewer; leave the full gate to integration review.

## Verification

All focused checks below passed on the new b009bbd8-based candidate, with zero
swaps. No full gate was run concurrently with the main reviewer.

```
env PATH=/bin:/usr/bin:/opt/homebrew/bin GOMEMLIMIT=768MiB GOMAXPROCS=N go test -race ./interp -run '^(TestBashPPFIFO|TestBashPPGoArmsBeforeBlockingFIFORedirect|TestBashPPGoFIFORedirectWaitsForTheWriter)' -count=50 -timeout=2m
```

| Check | Test duration | Maximum RSS, bytes |
| --- | ---: | ---: |
| Focused FIFO race x50, N=1 | 16.270s | 576503808 |
| Focused FIFO race x50, N=2 | 16.042s | 202276864 |
| Focused FIFO race x50, N=3 | 15.958s | 204226560 |
| Linux amd64 interp/syntax compilation | — | 527056896 |
| Windows amd64 interp/syntax compilation | — | 540377088 |
| FreeBSD amd64 interp/syntax compilation | — | 549158912 |

Compilation used the same PATH and memory bound, GOMAXPROCS=1,
GOARCH=amd64, CGO_ENABLED=0, `go test -c -p=1`, and separate output directories.
These are cross-build checks, not native execution on those platforms.

Three isolated behavioral tests were run with production files overlaid from
b009bbd8: persistent FIFO close, wrapped writer snapshot ownership, and
external-peer refusal. All three fail on that baseline and pass with the
candidate production files. The tracked suite also verifies cancellation and
fd cleanup, inode replacement, reader/writer-first readiness, retained reader
descriptor/data, persistent replacement, aliases/temporary restoration,
wrapped write-only duplication, EINTR retry/cancellation, and File cleanup of
a persistent FIFO opened before any go/chan declaration. The new tests use
state handshakes and direct ownership assertions, not sleeps/scheduler yields.

The canonical `TestRunnerRunConfirm` invocation SKIPPED because Bash 5.3 is
unavailable on the required PATH; no full Classic oracle pass is claimed.
`gofmt` and `git diff --check` pass. Classic/POSIX open helpers and the current
selected-Go/gate code are untouched. No merge, rebase, or push was performed.
