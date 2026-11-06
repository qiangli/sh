# S562 E3: explicit TypeScript timezone propagation

Regression proposal against sh `206d0f2da4d1791ec46976819fc6d79f5ead4bb7`.
No runtime fix or execution result is claimed by this regression-only draft.

Run #35 observed a frozen date rendering one day early and then observed
`process.env.TZ` as undefined despite the parent having `TZ=UTC`. The original
clock-dependent handbook case is `handbook-v2-Basics-L327`; retain its native
oracle and fenced results when rerunning the repaired candidate.

The interpreter supplies exported environment values to DiscoverEnvironment.
TypeScript discovery filters them through typeScriptLaunchEnvironment, which
omits TZ. TypeScript.configure supplies that filtered plan to both analyzer
and worker subprocesses. The omission also means TZ does not participate in
the existing environment fingerprint. JavaScript uses the same path.

This is not a promise of arbitrary environment inheritance: the filter
deliberately excludes loader settings and unrelated values. The proposed
contract is narrower: preserve an explicitly supplied TZ for TS/JS, including
an empty value; keep absent distinct from empty; keep NODE_OPTIONS, NODE_PATH
and secrets excluded. Existing plan.Env fingerprinting then identifies the
selected zone. Do not change the common helper without considering its Go,
native, Rust and PowerShell callers. A TS/JS-only addition at discovery is
the bounded repair proposed to the manager. Locale behavior is a separate
question and is not silently broadened by this change.

`polyglot/typescript_timezone_test.go` exercises environment discovery,
configured child environment and fingerprints without Node or provisioning.
It is expected to fail on the unfixed base for explicit timezone states.
The test does not execute its fixture executable.

On a claimed host with the candidate and managed TS compiler already present:

```sh
TZ=UTC "$BASHY_BIN" --bashsharp docs/s562/typescript-timezone-repro.bsh
TZ=Etc/GMT+12 "$BASHY_BIN" --bashsharp docs/s562/typescript-timezone-repro.bsh
```

Required outputs respectively:

```text
{"tz":"UTC","hour":0}
{"tz":"Etc/GMT+12","hour":12}
```

Repeat with the same body's `ts` opener changed to `js` (remove its return
type annotation), and through transpile/build/run using the exact matching
sh source and authenticated supervisor SDK. No in-fence assignment of TZ is
allowed in this regression. Record stdout, stderr, exit status, candidate and
toolchain identities. The fixed instant avoids a wall-clock dependency.

Focused source verification, on the test host only:

```sh
go test -count=1 ./polyglot -run '^TestTypeScriptExplicitTimezoneEnvironment$'
```

Then rerun existing environment discovery/sanitization tests and the original
handbook date case. A deterministic fixture that explicitly sets its own
timezone can validly test a separate fixture contract if applied equally to
native and fenced source, but cannot prove inherited-environment correctness
or replace this regression. No broad allowlist, secrets forwarding, timeout
change or transcript re-pin is part of the proposal.
