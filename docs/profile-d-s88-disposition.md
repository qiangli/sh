# Profile D S88 disposition — fc editor-session identities

Primary contract: [POSIX.1 Issue 7, 2016 Edition, `fc`](https://pubs.opengroup.org/onlinepubs/9699919799.2016edition/utilities/fc.html).
Scope: the twenty exact identities `fc` TP 4, 5, 7, 8, 10, 11, 17, 18, 19,
21, 22, 26, 27, 28, 29, 30, 31, 32, 33, 34 recorded non-PASS in the
`profile-d-s82-c287d82` journal (FAIL: 4, 5, 7, 10, 11, 17–19, 21, 22, 26,
28, 29, 31–34; UNRESOLVED: 8, 27, 30) and carried forward through the S85
Wave 1 closure.

## Revisions examined

The interpreter reducer was run against sh revision `98bd69cc` (the frozen
S88 launch SUT). A test-only interactive boundary reducer was then added and
run at `8d8b13a85bdc2c71474eeeaa7426528fdde9961b` on `novidesign.local` with
Go 1.26.5. Neither revision changes product behavior.

## The reducer

`interp/fc_s88_reducer_test.go` separates the two possible owners of the
S82 signature using only POSIX Issue 7 public behavior — no suite bytes,
fixture text, or journal content is read or reconstructed:

- **`TestFcS88EditorDrivenFromShellStdin` (shell-owned contract).** The
  classic editor transcript shape — a substitution, a write, a quit — is
  placed on the shell's own stdin, exactly where an interactive
  certification run leaves it, and `fc -e ed` is run under `set -o posix`
  with the real `ed(1)`. The reducer proves, at this revision, that the
  shell (1) hands its stdin to the spawned editor rather than consuming
  the transcript itself, (2) runs the editor on the temp file holding the
  selected history entry, (3) echoes the edited command to standard
  error, (4) executes the edited command with its status, and (5) records
  the edited command in history while dropping the `fc` invocation, with
  no transcript line ever reaching the command interpreter. Every
  Issue 7 editor-session requirement the S82 F cases could exercise
  holds.

- **`TestFcS88MisroutedEditorTranscriptSignature` (fixture-owned
  signature).** The same transcript is instead delivered as shell command
  input after the editor has already exited without consuming it. A fully
  conformant shell has no choice here: `s/world/goodbye/` and `q` are
  commands, they fail with exit 127 and diagnostics naming them — the
  journal's FAIL half — and, because an interactive shell records typed
  lines, they enter the history list, after which every later
  history-consuming listing or selection deterministically diverges from
  any expectation formed before the misroute — the journal's UNRESOLVED
  half. The complete S82 F-then-R signature is reproduced with zero `fc`
  defect involved.

- **`TestFcS88EditorSessionThroughInteractivePTY` (packaged boundary).**
  `interactive.Run` drives the real readline loop over a kernel PTY. The
  reducer waits for a fake editor's readiness marker before sending the edit
  transcript, proving that readline yields the terminal while the editor owns
  it; it then observes the edited command return through and execute in the
  packaged interactive shell. The PTY harness also answers readline's ANSI
  cursor-position queries, as a real terminal emulator does, so initialization
  cannot be mistaken for an `fc` failure.

The two interpreter tests and the interactive PTY reducer are green at the
revisions above. The authoritative focused Novi commands were:

```text
GOTOOLCHAIN=go1.26.5 go test ./interactive -run '^TestFcS88EditorSessionThroughInteractivePTY$' -count=1 -v
GOTOOLCHAIN=go1.26.5 go test ./interp -run '^TestFcS88' -count=1 -v
```

**No product red was reproduced, so no product-code patch is made.**

## Identity mapping

| Identity | Journal class | Disposition | Evidence |
| --- | --- | --- | --- |
| fc TP 4 | FAIL | fixture-owned | S82 journal records the transcript feeding `s/world/goodbye/` and `q` to the shell; the PTY reducer proves packaged interactive ownership handoff, interpreter test A proves the conformant editor session, and test B reproduces this FAIL signature from the misroute alone. |
| fc TP 5 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 7 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 8 | UNRESOLVED | fixture-owned (downstream) | Reducer test B: post-misroute history pollution makes later history-consuming assertions diverge with a conformant shell. |
| fc TP 10 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 11 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 17 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 18 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 19 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 21 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 22 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 26 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 27 | UNRESOLVED | fixture-owned (downstream) | Same as TP 8. |
| fc TP 28 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 29 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 30 | UNRESOLVED | fixture-owned (downstream) | Same as TP 8. |
| fc TP 31 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 32 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 33 | FAIL | fixture-owned | Same as TP 4. |
| fc TP 34 | FAIL | fixture-owned | Same as TP 4. |

No identity is classified product-fixed: the S85 Wave 1 repair included
in `98bd69cc` (POSIX single-substitution selector parsing,
`interp: parse POSIX fc substitution selector`) is adjacent hardening
with its own focused tests, but no journal evidence ties it causally to
any of these twenty identities, whose recorded signature is the editor
transcript misroute. No identity is still-open: each has a recorded
journal signature and a causal suite-free reproduction of that signature
with a conformant shell.

## Authority boundary

The licensed fixture and journal bytes were not read, copied, or reconstructed.
The classification granularity is the retained public S82 result record (the
identities and recorded misroute signature) plus the causal public-behavior
reducers. The packaged PTY test closes the previously unmeasured
readline-to-editor ownership boundary; the interpreter tests pin both possible
shell-side outcomes after that boundary.

Corroborating record: the earlier paired Profile D run
(`docs/profile-d-fc-disposition-2026-08-27.md`) shows 28 PASS / 24
UNTESTED / zero FAIL for `fc` on both this shell and the GNU Bash
control when the harness delivers its transcripts successfully.

## Honest residuals

- The focused reducers establish ownership of the retained misroute signature;
  they do not replace the next complete 117-set Profile D acceptance arm.
- The misroute reproduction surfaced a cosmetic divergence outside fc
  scope: a slash-containing missing command reports a Go-style
  `stat ...: no such file or directory` diagnostic rather than Bash's
  `<line>: No such file or directory`. Exit status 127 is correct; the
  message shape is not part of any of the twenty identities.
