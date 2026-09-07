# Native declarations at the shell boundary

`Program.ShellRegion` supplies an immutable view of present lexical binding IDs,
names, source types and constant flags to its backend. `shellexec` applies this
metadata only to a Bash++ runner, through `interp.WithDeclarations`. It does not
parse or execute declaration source, copy native object graphs, or recreate
channel/callable authority from strings. Actual interpreter declarations inside
a shell region keep precedence over the supplied view.

Unset is checked before mutation. A native `var` retains its value and produces
the interpreter's existing declaration diagnostic; a constant participates in
the existing readonly unset check. Constant assignment uses the existing
assignment diagnostic and DISCARD control flow. Only a refusal still escaping
that shell statement is reported to Program: `eval` can consume DISCARD, and
subshell refusal stays in that subshell. Program then stops its callable with
its existing shell control transfer, preserving status without printing twice.

The declaration view is scoped to each Run and copied as immutable metadata
into interpreter subshells. Independent Programs own their cells, views and
refusal records. Ordinary Bash/POSIX and calls without metadata retain their
existing behavior. This hook governs declaration deletion and constant writes;
it does not claim to complete every rich-value attribute or EXIT-trap lexical
capture rule.

Tests compare complete live interpreter output/status with the backend for
ordinary and compound writes, eval, subshells, unset and independent concurrent
Programs. Whole compiler tests build native artifacts, remove generated source,
and compare direct and subshell unset behavior. Constant whole-compiler proof
also requires the compiler's separate constant shadow registration hook.
