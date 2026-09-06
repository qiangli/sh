---
id: 141323c099a4
kind: task
title: Bash++ compact go closure requires a shell ';' separator
seq: 20
status: done
priority: p2
created: 2026-09-06T10:21:21.014472Z
sprint: 115
closed: 2026-09-06T14:01:27.967066Z
---

Observed on sh master 586f87b6 (parse-only).

    go func() { echo hi; }()   ok
    go func() { echo hi }()    FAIL  a command can only contain words and
                                     redirects; encountered '('
    go func() {
        echo hi
    }()                        ok

'{ echo hi }' is invalid shell (a brace group needs a separator before '}')
but is idiomatic Go, so the Go-spelled one-line closure is currently
unavailable inside a committed Go region. Multi-line and semicolon-terminated
spellings both work, so this is specifically the separator rule, not the
closure or the call suffix.

For a Go-1.27-shaped profile this is a superset gap: the shape a Go author
would write is rejected while the shell-flavoured spelling is accepted.

SCOPE NOTE: the fix must stay inside a committed Go region (bashppFuncDepth >
0). Relaxing the separator generally would change stock shell parsing, which
the Class-R discipline exists to prevent. Worth a rollback and byte-for-byte
reprint case alongside the existing recognizer tests in
syntax/bashpp_chan_test.go.

PROVENANCE: found while checking whether killed weave run sh#51 had been
superseded. That run patched this by breaking the call-expression loop on '}'
when inside a Go region; the approach is a starting point, not a reviewed fix,
and the rest of that workspace is superseded.
