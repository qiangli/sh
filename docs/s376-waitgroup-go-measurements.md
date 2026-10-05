# S376 resident `wg.Go` lowering (Story #1549)

Cause: a statement `wg.Go(f)` made the package-wide resident-sync planner decline,
so every Mutex/WaitGroup in e.g. `examples/mutexes/mutexes.bsh` stayed on the native
bridge (baseline on a dev Mac: 1,500 lock pairs did not finish in 120 s).

Change: a certified statement-level `x.Go(f)` lowers to
`func(wgr *T, wgf func()){ wgr.Add(1); go func(){ defer wgr.Done(); wgf() }() }(&x, f)`:
receiver then `f` evaluated once as arguments, `Add` only after both evaluated,
`Done` on the captured receiver even if the variable/field is reassigned.
Also: calls to package functions / function-literal-only locals no longer decline
the certificate; pointer cells holding resident sync objects are shared into `go`
tasks; `assignable` answers for `*sync.X` targets host-side.

Tests (bounded, dev): `TestS376ResidentWaitGroupGo{Differential,Certificates}`
(differential vs `go run`: receiver pointer/field reassignment, argument side effect
and panic ordering, pointer params, embedded, field), plus S374/S376 resident-sync
regressions and `./gosource`: all pass.

Not measured here: the production mutexes input (3 x 10000) and the 20 s stage limit
(unchanged). An earlier dev probe on the real file, before the callee rule, showed
cert=0 and timed out at 120 s; it is retained as the raw failure. The post-rule
real-file timing is NOT measured on dragon; the manager owns remote GBE/root runs.
ken/chan.go and issue79186.go exact IDs are not run here.
