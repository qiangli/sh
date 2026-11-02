---
id: de21afd1cbc8
kind: task
title: S557 diagnose Windows command-substitution redirection ambiguity
seq: 187
status: todo
created: 2026-11-02T02:42:57.70123Z
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Hosted Bash53 run41373848558 at umbrella2296c359 reports Windows84/86, with glob-test and redir failing; Linux/macOS86/86. Redir raw output has one difference at outputline166: expected foo, got ambiguous redirect for command substitution echo of variable a at redir11.sub:65. Preserve original fixture/assertions and all86 denominator; inspect exact fixture and author a minimized regression before product changes. No foreign suite on development host. Root assigns implementation after read-only diagnosis; no guessed normalization or skips.


Read-only fixture context: the failing redirect follows an integer declaration
and resets the variable to 2 before a command-local assignment of 9. The
redirection target comes from command substitution. The adjacent eval variant
still prints the expected output; all other 183 output lines match. A minimal
authored starting point for verification (not yet executed) is:

```bash
declare -i target=2
target=9 printf '%s\n' marker >&$(printf '%s' "$target")
```

Verify expansion ordering, command-substitution bytes and stderr routing on
native Windows against the unchanged GNU expectation; do not assume an errno
normalization or timing cause. Original suite output must remain 84/86 until a
new complete hosted run proves otherwise. The known glob-test filename failure
is separate from this one-line redirection difference.
