# Sprint 162 interp-control-2 findings

| root | first cause | mechanism | status |
|---|---|---|---|
| `testdir:chan/select3.go` | expression-position recover lacked a value path | typed recover interface | fixed in `2a337899` |
| `testdir:fixedbugs/issue26094.go` | expression-position recover lacked a value path | typed recover interface | fixed in `2a337899` |
| `testdir:fixedbugs/issue43942.go` | expression-position recover lacked a value path | typed recover interface | fixed in `2a337899` |
| `testdir:fixedbugs/issue48898.go` | expression-position recover lacked a value path | typed recover interface | fixed in `2a337899` |
| `testdir:fixedbugs/issue73916.go` | nested deferred-call recovery is direct-only | negative-set preservation | fixed in `2a337899` |
| `testdir:fixedbugs/issue73916b.go` | nested deferred-call recovery is direct-only | negative-set preservation | fixed in `2a337899` |
| `testdir:fixedbugs/issue74379.go` | recovered interface unavailable to comparison | typed recover interface | fixed in `2a337899` |
| `testdir:fixedbugs/issue74379b.go` | recovered interface unavailable to comparison | typed recover interface | fixed in `2a337899` |
| `testdir:fixedbugs/issue74379c.go` | recovered interface unavailable to comparison | typed recover interface | fixed in `2a337899` |
| `testdir:interface/noeq.go` | recovered interface unavailable to comparison | typed recover interface | fixed in `2a337899` |

## Requests to other seams

None.
