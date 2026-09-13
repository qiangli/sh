---
id: e838e8895341
kind: task
title: 'Sprint 162.6s: Bash++-ON classic regression — product fix in sh (cprint output, procsub hang under activation)'
seq: 96
status: todo
priority: p0
created: 2026-09-13T02:07:06.732928Z
sprint: 162
---

Product half of S162.6 (bashpp-tests #70 5c6251698ece). tools/classic-gate.sh on the published candidate at the darwin venue: Bash++ OFF 86/86, Bash++ ON 84 + cprint FAIL (output differs from cprint.right) + procsub TIMEOUT (60 s). Once S162.6 has the Linux verdict: root-cause each in sh — the Bash++ activation path must not change Classic behaviour (the isolation contract); fix from an outside-corpus reproducer under the owning package's testdata/sprint162/classic/ with the negative set; focused go test -count=1 + go test -short ./... + the container gate OFF and ON both 86/86 on Linux. Never a timeout raise; never keyed to a fixture. Trailers: Sprint: #162 / Story: #<this seq> / Story-ID: <this id>.
