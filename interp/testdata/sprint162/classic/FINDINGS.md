# Sprint 162 — lane `classic-fix` (S162.6s, sh #96, Story-ID e838e8895341)

Seam: `interp/runner.go` (ProcSubst), `interp/bashpp_fifo*.go`,
`interp/bashpp_concurrency.go` (`bashPPTaskOpen`). Test:
`interp/sprint162_classic_test.go` runs every `*.sh` here under `LangBash`
and `LangBashPP` and requires byte-identical stdout, stderr and exit status
(the isolation contract), each run bounded by a deadline so a hang is a
FAIL; `TestSprint162BashPPFIFOAfterTask` pins the rule boundary once the
File has had a task; `TestBashPPFIFOPublishedPeerReleasesAnnouncedOpener`
and `TestBashPPFIFOTaskRedirectOverProcessSubstitution` in
`bashpp_fifo_unix_test.go` pin the release window.

## Rows

| root | first cause | mechanism | status |
| --- | --- | --- | --- |
| bash-5.3 `procsub` (line 74, `done 3< <(echo x)`) | the shell's redirection over a process-substitution FIFO waited for a peer *registered* in the File's task group; the ProcSubst goroutine opened the FIFO natively and never registered — `bashPPFIFOOpen` selects forever (darwin: Go "all goroutines are asleep" fatal; Linux: the 60 s TIME) | A′ + B | fixed (A′: process-substitution endpoints published; B: blocking open before the first task) |
| bash-5.3 `histexpand` (`histexp4.sub` line 20, `cat < <(echo !!)`) | same first cause; Linux native ON gate FAIL (`classic-1/logs/full-on.log` on the leaf host, sibling lane), darwin: the deadlock fatal | A′ + B | fixed by the same commits (not in the brief; found in the sibling lane's Linux record) |
| `procsub-redir.sh` (`read -r x < <(echo hello)`) | as `procsub` | A′ | fixed |
| `procsub-redir-out.sh` (`echo out > >(cat)`) | as `procsub`, writer side | A′ | fixed |
| `procsub-redir-fd.sh` (the corpus shape, `3< <(echo x)` + `read -u3`) | as `procsub` | A′ | fixed |
| `fifo-external-peer.sh` (`mkfifo p; cat p & echo x > p`) | the shell's write open of a named FIFO required a registered reader; `cat` is external | B | fixed |
| `procsub-external-control.sh` (`cat <(echo viacat)`) | positive control — passed before; **would have regressed under the brief's mechanism A** (see below) | — | control, green |
| `fifo-subshell-peer-control.sh` (`( echo > p ) & read < p`) | negative-set control | — | control, green |
| bash-5.3 `cprint` | **not reproducible.** darwin native through `bin/bash53suite` (the same runner the container bakes): base (umbrella pin, sh `1ba8bd26` + todo) OFF PASS / ON PASS; this branch OFF PASS / ON PASS. Linux native (leaf host, sibling lane `classic-1`): OFF PASS / ON PASS, focused and full. The only venue that recorded a FAIL is the podman container gate at the darwin venue; this host has no podman machine, so that venue cannot be re-run here | — | not reproduced; request below |

## Mechanisms

**A′ — process-substitution endpoints are published, not rendezvoused.**
The brief's mechanism A (open the ProcSubst end through `bashPPTaskOpen`)
was implemented first and broke `procsub-external-control.sh` under Bash++:
the substitution's end then waited for a registered peer, and `cat` is
external. A process substitution cannot know whether its consumer is the
shell or a native process, so its end takes Bash's blocking open (the kernel
pairs it with whichever peer arrives) and is then *published* to the group
(`bashPPFIFOPublish`): an already-released entry that a registered opener
of the opposite end matches. Classic has no group and takes the identical
open.

Publishing after a completed open has a window: the shell's rendezvous
acquisition is what releases the substitution's blocking open, and the
substitution can then write, close and unregister before the shell
registers its descriptor. So a rendezvous opener now *announces* itself
(`fifoPending`) before acquiring, and a published endpoint may release an
announced opener — its own open completed, hence the announced opener's
descriptor exists and cannot read a premature end-of-file. A rendezvous
opener still never releases an announced peer (it would close its probe,
or read end-of-file, before the peer holds a descriptor) — the existing
rule, unchanged.

**B — the registered-peer rule applies once the File has had a task.**
`bashPPFIFORendezvous`: a runner inside a task, or a group whose
`nextTask > 0`, uses the rendezvous; before that the File is single-threaded
apart from its own shell copies and process substitutions, all of which now
open natively, so `bashPPTaskOpen` takes Bash's blocking open and publishes
the descriptor. The early-descriptor snapshot constraint is kept: the
published entry is registered (matched) in `c.fifos`, so
`TestBashPPFIFOFileOwnsPreTaskPersistentDescriptor` still sees the pre-task
`exec 8<>fifo` descriptor registered and a later task snapshot clones it.
Decision recorded: "has had a task" is monotonic — a task that has run may
still hold descriptors, so the rule stays in force for the rest of the File;
a Bash++-shaped script that launches a task and then opens a named FIFO
against an external peer is refused as before
(`TestSprint162BashPPFIFOAfterTask/fifo-external-peer-unsupported`).

Known, accepted: with a published endpoint registered under the ProcSubst
subshell, `exec >file` inside a `>(…)`/`<(…)` body retires the endpoint at
the `exec` (the group's reconcile), where Classic keeps it open until the
body ends. Both close before anything observable differs (probed: identical
output in both modes); GNU Bash closes at the `exec` too.

## Requests to other seams

- **bashpp-tests (classic lane, #70):** `cprint` — if the container gate
  records it again, publish the runner's want/got pair
  (`BASH53_DEBUG_DIR`) with the candidate identity; nothing in `sh`
  reproduces it natively on either platform.
- **bashpp-tests (classic lane, #70):** add `histexpand` to the classic-ON
  record: it is ON-only on Linux native (`classic-1`), same first cause as
  `procsub`, and follows this fix.
