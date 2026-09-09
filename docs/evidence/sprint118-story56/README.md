# Sprint #118 / Story #56 / Story-ID 3ef468f4e831 — evidence

Correction of the held testing-callback recovery patch
`121259416391850a32f8d219088c90bea7973298`, on the typed-nil + Read base
`f89576fe` (Story #54, `c3a60493cde9`).

## Manager BLOCKER

The held patch reduced a returned callback's status with a blanket condition:

```go
if r.exit.code == 1 && !r.exit.exiting && !r.exit.fatalExit && !r.bashPPPanicking() {
	return 0
}
```

Any status 1 standing at a callback boundary became a pass, so a native or
runtime failure reporting 1 would have been hidden. The patch's own prose
argued this was safe because "every Bash++ diagnostic reports
bashPPPanicStatus (2)" — an argument about the current diagnostics, not a
property the condition enforces.

## Correction

The reduction is now keyed to provenance, not to the status value.

- `recover` stamps the status it reports: `Runner.bashPPRecoverSeq` is a
  monotonic counter, and the value it takes is recorded on
  `exitStatus.recoverSeq` (`interp/bashpp_panic.go`). Only the "nothing to
  recover" answer is stamped.
- `exitStatus.clear()` drops the stamp, because a cleared status is no longer
  recover's answer (`interp/api.go`).
- `runFunction` samples the counter immediately before invoking one callback
  and passes it as `recoverMark`. `testingCallbackStatus(recoverMark)`
  discards a status 1 only when the stamp is non-zero, is the current one
  (recover reported it and nothing has reported since), and was issued while
  THAT callback ran — and only with no terminating condition pending
  (`interp/gosource_testing.go`).

An unstamped status 1, a stamp superseded within the callback, a stamp left by
an earlier callback, every terminating condition, and every status other than 1
are all returned unchanged.

## Discriminating evidence

`TestTestingCallbackStatusProvenance` (`interp/gosource_testing_internal_test.go`)
holds the status at 1 and varies only the provenance. Against the blanket
condition three of its cases fail; against the narrowed one all pass:

```
--- FAIL: TestTestingCallbackStatusProvenance/unstamped_status_one
--- FAIL: TestTestingCallbackStatusProvenance/stamp_superseded_within_the_callback
--- FAIL: TestTestingCallbackStatusProvenance/stamp_issued_before_this_callback
```

`TestTestingCallbackStatusPanicStillFails` pins the
`recover(); t.Log(); panic("deliberate")` shape: a panic unwinding out of the
body fails its callback even though a recover stamped the runner first.

## End-to-end shapes

`TestGoSourceTestingRecoverProvenance` (`interp/gosource_testing_test.go`) runs
the same rule through the real scheduler. Measured outcomes:

| body | outcome |
| --- | --- |
| `TestGuardOnly` — `defer func(){ recover() }()` | passes |
| `TestGuardThenDeliberatePanic` | `exit status 2`, stderr `panic: deliberate` |
| `TestGuardThenFatal` | fails via the capability (`t.Fatal`) |
| `TestGuardThenBridgeFailure` — nil-channel send | `exit status 1` |

Recorded honestly: this test does **not** by itself distinguish the narrowed
condition from the blanket one. Each of its failures is already carried by
`exit.err`, by the panic status, or by the capability rather than by a bare
residual status 1, so the blanket form passes it too. The discrimination is the
unit test above.

## Ten-root corpus replay (typed-nil + Read base)

`corpus-replay-typednil-read.txt` — full output of:

```sh
go test -tags gosource_testing_corpus ./interp/ \
  -run 'TestGoSourceTestingErrorsCorpus' -count=1 -timeout 900s -v
```

Discovery registers the same ten roots. All ten are invoked. Three pass
(`TestNewEqual`, `TestErrorMethod`, `TestJoinReturnsNil`); seven fail and the
gate still fails. Every one of the seven fails ahead of any scheduler callback,
in the shared value model, and none is a candidate for the reduction:

| root | diagnostic |
| --- | --- |
| `TestJoin` | status 2 — `BASHPP-EASSIGN-MISMATCH: cannot use value as error` |
| `TestJoinErrorMethod` | status 2 — `BASHPP-ERANGE-TYPE` / `BASHPP-EEXPR-FORM: unsupported scalar expression *syntax.BashPPCompositeLit` |
| `TestIs` | status 2 — `BASHPP-ECOLLECTION-ELEMENT` / `BASHPP-EEXPR-FORM: unsupported scalar expression *syntax.BashPPFuncLit` |
| `TestAs` | status 2 — `BASHPP-ECOLLECTION-ELEMENT` / `BASHPP-EEXPR-NIL: nil is not a scalar` |
| `TestAsValidation` | status 2 — `BASHPP-ECOLLECTION-ELEMENT` / `BASHPP-EEXPR-NIL: nil is not a scalar` |
| `TestAsType` | status 2 — `BASHPP-ECOLLECTION-ELEMENT` / `BASHPP-EEXPR-NIL: nil is not a scalar` |
| `TestUnwrap` | `gosource: original method wrapped.Unwrap is not supported by dependency transport` |

No original `Test`/subtest/function/method body was edited; every fixture used
for the repair is separately authored. No exclusions, no comparator changes, no
native-body forwarding.
