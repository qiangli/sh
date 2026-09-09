# Native dependency channels

Sprint: #118; Story: #51; Story-ID: 825f8083451e

GoSource receive/send and all-native select retain actual dependency channel
handles in their authenticated session. Original expressions, operands, case
bodies, functions and goroutines execute in the interpreter. Only imported
operations and channel communication run in the dependency helper.

Each select evaluates operands and send values once, in source order. It sends
one complete case list; the helper validates every type, direction and send
value before invoking one `reflect.Select`. Duplicate cases are legal. A
nonblocking whole-list probe either commits and returns exactly one winner or
returns default without communication. If no case won and the source has no
default, the interpreter releases its goroutine launch handshake and submits
those same evaluated values for a blocking atomic selection. No speculative
per-case receives, replay queues or losing-arm sends exist.

A session cancellation channel participates only as a shutdown arm. Closing the
transport first closes that channel, then joins request handlers. This wakes
blocked receives when original main returns (including stopped timer receivers),
avoiding a helper deadlock dump. Host cancellation retains the existing bounded
whole-session shutdown/kill policy. Reset starts a fresh session; old handles
are rejected before requests reach the helper.

A reflection-only channel type query admits native channels as interpreted
function parameters/results with exact Go assignability and channel direction.
A native field receiver such as `timer.C` is evaluated through typed member
access, never through scalar string conversion.

## Deliberate boundaries

Mixed local/native select remains an explicit error before communication. The
original GbE tickers example therefore remains unsupported: its select combines
local `done` with native `ticker.C`. Interpreter-owned mutable slice/map/pointer
and callback payloads cannot be retained by native channels; native-owned
handles remain in their original session. Named channel types whose channel
kind is not represented by current metadata are not guessed. Native channel
`make`, `close`, range and a unified local/native channel runtime are subsequent
work, not acceptance claimed by this change. Invoking a returned named native
function such as `context.CancelFunc` remains an adjacent callable-routing
limitation; `context.WithTimeout(...).Done()` receives are covered independently.

## Verification

Sixteen deterministic authored programs compare raw stdout/stderr in native
Go, the actual Runner, and a real compiled artifact after original/generated
sources and dependency fixture sources are removed. Controls cover unselected
state, duplicate cases, no self-match, exact operand/RHS order, later RHS panic,
concurrent imported sender and interpreted receiver, typed scalar/struct copies,
nil/default/closed behavior and recover. The unchanged retained GbE timers
source is SHA-bound and uses the same three-mode check.

Actual-session adversaries reject invalid later cases and stale handles without
consuming an earlier ready value. Cancellation tests block native receive/send/
select on nil, cancel, join, and reuse Runner.Reset with a new working channel.
The existing classic directional/local select controls remain in the gate.

The unchanged Tour default-selection source executes successfully in all three
modes; its real elapsed timestamps differ. Raw diagnostic streams are retained,
without claiming raw equality or adding a new timing normalizer.

Measured gates for the submitted slice: full native-channel race gate passed in
81.270 s; added retention adversaries passed under race in 5.462 s; typed After
and context Done three-mode controls passed under race in 7.755 s. Existing
classic/local channel and typed send/receive regressions passed in 121.199 s.
`TestRunnerRunConfirm` remains failing against the host Bash baseline (9.932 s);
its raw log is retained and no global regression PASS is claimed.
