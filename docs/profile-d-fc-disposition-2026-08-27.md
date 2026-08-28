# Profile D `fc` Disposition — 2026-08-27

Primary contract: [The Open Group POSIX.1 Issue 7, 2016 Edition `fc`](https://pubs.opengroup.org/onlinepubs/9699919799.2016edition/utilities/fc.html).

## Ownership and interface

`fc` is an intrinsic shell utility implemented in `interp/history.go`; it does
not belong to the coreutils applet registry. The POSIX route accepts all three
required forms:

```text
fc [-r] [-e editor] [first [last]]
fc -l [-nr] [first [last]]
fc -s [old=new] [first]
```

This is the complete required option surface: `-e` with its mandatory
`editor` option-argument, plus `-l`, `-n`, `-r`, and `-s`. The `first` and
`last` operands accept positive command numbers, negative offsets, and command
prefix strings. The implementation also accepts `-e -` as the standardized
edit-without-editor form and honors the `--` special token.

Clause-focused source tests cover separated `-e` arguments, `FCEDIT` and the
POSIX `ed` default, the three form grammars, first-occurrence `old=new`
replacement, numeric and prefix selection, omitted operands, forward and
reverse ranges, `-l`, `-n`, `-r`, `-s`, multiline listing indentation,
history-record replacement, editor failure, and re-executed command status.
The principal evidence is `TestFcPosixEditorAndArgs`,
`TestFcIssue7FormValidation`,
`TestFcIssue7FirstSubstitutionAndMultilineListing`,
`TestFcReverseEditRange`, `TestFcListPosixOmitsModifiedMarker`,
`TestFcIssue7ReexecutionReturnsCommandStatus`, and
`TestFcIssue7ListRangeSelectsNewestCommand`.

## Deliberate POSIX-over-Bash divergence: newest command number

Bash's `fc_gethnum` bounds a positive command number against the newest
selectable history index *exclusively*. Naming that newest number therefore
falls out of range and is silently clamped -- to the oldest entry for `first`,
to the newest for `last`. Over a three-command history, real GNU Bash 5.3
prints:

```text
$ fc -l 3 1
1	 : one
$ fc -l 3 3
1	 : one
2	 : two
3	 : three
```

POSIX Issue 7 imposes no such exclusion. `first` and `last` are command
numbers "displayed with the `-l` option", and a `first` newer than `last`
lists the range in reverse. Command 3 is displayed by `fc -l`, so `fc -l 3 1`
must list 3, 2, 1 and `fc -l 3 3` must list only command 3.

`interp/history.go` resolves this in favor of the standard **only when POSIX
mode is on**: the bound is inclusive (`n > idx`) under `set -o posix` and stays
exclusive (`n >= idx`) otherwise. Bash mode is left byte-for-byte identical to
GNU Bash 5.3, since this engine's default route is a Bash drop-in and the
clamp is observable Bash behavior rather than a defect. Numbers genuinely past
the newest command (`fc -l 4 1` above) still clamp in both modes.

`TestFcIssue7ListRangeSelectsNewestCommand` asserts both columns of this
divergence in one table; its bash-mode expectations were captured from GNU
Bash 5.3.15 and must not be re-aligned to the POSIX column.

## Profile D result

The paired run contains 52 `fc` assertions. Bashy and the GNU control have the
same disposition: 28 PASS and 24 UNTESTED, with zero FAIL, UNRESOLVED, or
unsupported results. The 24 non-pass assertions are 1, 2, 3, 7, 13, 21, 25,
36 through 46, 48 through 50, and 52 through 54. The journal itself labels
these as “No portable test”, “No portable way to test”, or “Test not yet
implemented”. They are suite capability gaps, not evidence of an `fc` defect.

The executed semantic assertions pass on both sides, including interactive
listing and editing, history insertion/suppression, editor failure,
redirection, mandatory option-argument separation, utility syntax checks,
editor selection, range/default behavior, the last-16 default, reverse ranges,
and first-only substitution. Consequently the earlier ranked “20 TP” summary
must not be interpreted as 20 `fc` failures or used to justify behavior
changes; the raw paired journals establish the exact 28/24 disposition above.

## Honest residuals

The Profile D run does not establish command-number wrap behavior, signal
behavior, locale-specific diagnostics, portable `HISTFILE` lifecycle
behavior, or deletion behavior caused by history-size limits. Those remain
explicit evidence gaps. `HISTSIZE` stifling, `HISTFILE` loading/writing, and
`fc -s` re-execution do have local source tests, but local tests do not alter
the certification suite's UNTESTED disposition.
