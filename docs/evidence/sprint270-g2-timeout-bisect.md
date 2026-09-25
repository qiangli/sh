# Sprint 270 G2: bridge timeout bisect

Date: 2026-09-24 (America/Los_Angeles)

Story: `609c89bfa598` (Sprint 270 G2). Coordinated with Sprint 275 manager
`codex-sprint275-manager`; no story ownership was transferred.

## Verdict

Both requested top-level tests are red at sprint-269 revision
`5d4c0e3dfb530ff1013e52243011bbe04b625e7f` and at current base
`f3729e1826ba11223beaf1624a6ebef05a2e7059`. The current base contains the
preserved G2 speed patch `638d11eb7753742e6a13dc164ebce26f0a60c6ac`
through merge `f3729e18`; that patch does not make either test green.

The first bad commit for both test IDs is:

```text
b8b74f95fb7443f5255da9837986889345f59da7 interp: recursively spell native bridge types
parent 309406875c568a534dadae9948bec0d32f75e49e interp: integrate complex scalar and storage semantics
```

This is a native bridge/callback regression, not interpreter speed. At the
first bad commit, both tests fail well below the bound with
`gosource: callback has no original owner or receiver` in
`generic-instantiation/instantiated_stringer`. Later revisions turn this same
path into a wait in `bashPPNativeSession.request`, producing the observed
deadline rather than establishing a separate speed-first cause.

## Reproduction protocol

Each revision's `interp` test binary was compiled once with the full tag:

```sh
go test -tags full -c -o /tmp/s270-g2-diagnostic.2YBFxG/current-interp.test ./interp
go test -tags full -c -o /tmp/s270-g2-diagnostic.2YBFxG/sprint269-interp.test ./interp
```

Each top-level test then ran in its own process from the revision's `interp/`
directory. `test2json` retained the subtest, error, timestamp, and elapsed
records. The Go watchdog was set below the independent 60-second hard kill so
the failure record could be emitted before the ceiling:

```sh
set -o pipefail; { /usr/bin/time -p /opt/homebrew/bin/gtimeout --signal=KILL 60s go tool test2json -t -p interp /tmp/s270-g2-diagnostic.2YBFxG/current-interp.test -test.v=test2json -test.run '^TestGoSourceBridgeGenericInstantiation$' -test.count=1 -test.timeout=59s; } 2>&1 | tee /tmp/s270-g2-diagnostic.2YBFxG/current-TestGoSourceBridgeGenericInstantiation.jsonl
set -o pipefail; { /usr/bin/time -p /opt/homebrew/bin/gtimeout --signal=KILL 60s go tool test2json -t -p interp /tmp/s270-g2-diagnostic.2YBFxG/current-interp.test -test.v=test2json -test.run '^TestSprint153Evaluator$' -test.count=1 -test.timeout=59s; } 2>&1 | tee /tmp/s270-g2-diagnostic.2YBFxG/current-TestSprint153Evaluator.jsonl
set -o pipefail; { /usr/bin/time -p /opt/homebrew/bin/gtimeout --signal=KILL 60s go tool test2json -t -p interp /tmp/s270-g2-diagnostic.2YBFxG/sprint269-interp.test -test.v=test2json -test.run '^TestGoSourceBridgeGenericInstantiation$' -test.count=1 -test.timeout=58s; } 2>&1 | tee /tmp/s270-g2-diagnostic.2YBFxG/sprint269-TestGoSourceBridgeGenericInstantiation.jsonl
set -o pipefail; { /usr/bin/time -p /opt/homebrew/bin/gtimeout --signal=KILL 60s go tool test2json -t -p interp /tmp/s270-g2-diagnostic.2YBFxG/sprint269-interp.test -test.v=test2json -test.run '^TestSprint153Evaluator$' -test.count=1 -test.timeout=58s; } 2>&1 | tee /tmp/s270-g2-diagnostic.2YBFxG/sprint269-TestSprint153Evaluator.jsonl
```

The sprint-269 bridge run was repeated at a 58-second Go watchdog because the
first 59-second attempt reached the independent 60-second ceiling during
test-binary startup and therefore emitted no Go failure record. No result from
that attempt was used.

## Exact base records

Current base `f3729e1826ba11223beaf1624a6ebef05a2e7059`:

```json
{"Time":"2026-09-24T17:32:21.07415-07:00","Action":"output","Package":"interp","Test":"TestGoSourceBridgeGenericInstantiation/instantiated_stringer.go","Output":"panic: test timed out after 59s\n"}
{"Time":"2026-09-24T17:32:21.076162-07:00","Action":"fail","Package":"interp","Test":"TestGoSourceBridgeGenericInstantiation/instantiated_stringer.go","Elapsed":59.03}
{"Time":"2026-09-24T17:33:25.852181-07:00","Action":"output","Package":"interp","Test":"TestSprint153Evaluator/spread_call_args/single","Output":"panic: test timed out after 59s\n"}
{"Time":"2026-09-24T17:33:25.858333-07:00","Action":"fail","Package":"interp","Test":"TestSprint153Evaluator/spread_call_args/single","Elapsed":59.141}
```

Wall records were respectively `real 59.21` and `real 59.53` seconds.

Sprint-269 `5d4c0e3dfb530ff1013e52243011bbe04b625e7f`:

```json
{"Time":"2026-09-24T17:35:56.738117-07:00","Action":"output","Package":"interp","Test":"TestGoSourceBridgeGenericInstantiation/instantiated_stringer.go","Output":"panic: test timed out after 58s\n"}
{"Time":"2026-09-24T17:35:56.739883-07:00","Action":"fail","Package":"interp","Test":"TestGoSourceBridgeGenericInstantiation/instantiated_stringer.go","Elapsed":58.027}
{"Time":"2026-09-24T17:36:59.76931-07:00","Action":"output","Package":"interp","Test":"TestSprint153Evaluator/method-mirror/marshaler_invoked","Output":"panic: test timed out after 58s\n"}
{"Time":"2026-09-24T17:36:59.77173-07:00","Action":"fail","Package":"interp","Test":"TestSprint153Evaluator/method-mirror/marshaler_invoked","Elapsed":58.026}
```

Wall records were respectively `real 58.12` and `real 58.09` seconds.

## Bisect

The bisect command compiled once per candidate and ran exactly one top-level
test process with the same bound:

```sh
git bisect run zsh -c 'go test -tags full -c -o /tmp/s270-g2-diagnostic.2YBFxG/bisect.test ./interp || exit 125; cd interp || exit 125; /opt/homebrew/bin/gtimeout --signal=KILL 60s /tmp/s270-g2-diagnostic.2YBFxG/bisect.test -test.v -test.run "^TEST_NAME$" -test.count=1 -test.timeout=58s'
```

`TestGoSourceBridgeGenericInstantiation` used verified good
`9f34d0bc4afdd841ea52b027b2f3cba98a66e9b9` (3.70 seconds) and bad
`5d4c0e3d`. `TestSprint153Evaluator` was red at its introduction commit, so
the bisect did not incorrectly use it as good: `04c3e935a7a344c7e7281602d21ca7a87061932b`
was separately verified green and used as the good endpoint.

Both bisects produced the same decisions after their respective starts:

```text
good e80926d8f6cb9ecd98c6eedfa6d369bff196b2e7
bad  c73a688add5d4c3fc6a094aaa60826404d17c773
good 725b7a27da33d6ffadfb93c1537af132fd85cdfa
bad  1054eb039c4f656eb5aeea110faec393779408b5
bad  0c29327fc6cc3e9709110ee0056544ccb3b6344b
good 7525023b8278fee4a9e757797ef403eedcb3d78b
bad  e01da46a6fa230b52949968b299b1c5f1acee8c4
bad  1294f8f2bf194f40f4a44956d6b4369a4e7d8289
bad  e0bdbec76118f621a53a7f5a0033e5ee977671ea
bad  b8b74f95fb7443f5255da9837986889345f59da7
first bad b8b74f95fb7443f5255da9837986889345f59da7
```

Direct parent/bad confirmation, again one top-level test per process:

```text
309406875c568a534dadae9948bec0d32f75e49e
  TestGoSourceBridgeGenericInstantiation: PASS, package elapsed 3.529s
  TestSprint153Evaluator: PASS, package elapsed 46.236s

b8b74f95fb7443f5255da9837986889345f59da7
  TestGoSourceBridgeGenericInstantiation/instantiated_stringer.go:
    FAIL 1.11s, gosource: callback has no original owner or receiver
  TestGoSourceBridgeGenericInstantiation: FAIL 3.27s (package 3.633s)
  TestSprint153Evaluator/generic-instantiation/instantiated_stringer:
    FAIL 5.67s, gosource: callback has no original owner or receiver
  TestSprint153Evaluator: FAIL (package 41.685s)
```

All bisecting occurred in a detached temporary worktree. Both `git bisect
reset` operations completed, the temporary worktree was removed, and the live
branch was never switched or placed in bisect state. No fixture, timeout, or
production file was modified.
