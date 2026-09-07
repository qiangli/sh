# Native channels with explicit task ownership

This slice adds channel runtime operations and emitter helpers. Channels remain
ordinary Go `chan T` values, with directional send/receive signatures and native
value copying, element identity and comma-ok results. A separately threaded
`ChannelScope` records ownership and synchronization. There are no package-global
contexts, session singletons, thread locals or goroutine-ID lookups.

The helpers require `RuntimeContext{Context, Session, Channels}` expressions.
Each private callable implementation must receive them explicitly. Public
function signatures can retain their ordinary channel parameters and results
through wrappers, but a task must call its private child-context implementation.
Calling a public wrapper that creates a fresh context would lose cancellation.

## Native runtime operations

`MakeChannel[T](scope, capacity)` creates and registers a native channel with the
profile's capacity bounds, 0 through 65536. `Send`, `Receive`, and `SelectChannels`
take an explicit context, session and scope. They check preexisting cancellation,
arm the session's launch handshake, and include context, task-group and scope
cancellation in every blocking wait. Nil channels remain blocking operands and
can still be canceled. Empty select is likewise cancellable.

`CloseChannel` wakes registered blocked sends and waits for their unregister
handshake before closing the native channel. This avoids the native concurrent
send/close data race. Typed code must use these operations consistently; a bare
native close or send bypasses the handshake. Receive and close retain buffered
values and native zero/comma-ok behavior.

Select operands are captured in source order before selection. Typed case
constructors preserve channel direction and send assignability, while
`reflect.Select` chooses among ready cases. Only the selected arm introduces
its receive bindings. A closed send is represented as one ready failing case,
rather than an unconditional preflight error suppressing other ready cases.
All prepared sends unregister on every return path.

`ChannelAbort` lets a native function with an unchanged public result signature
unwind a channel error. `ChannelTask` must wrap every task entry to convert this
specific unwind into its original error before the session's generic panic
boundary runs. That preserves cancellation ranking. Other panics still reach
the existing task panic handling. The program entry boundary must also catch
this unwind and apply the language's diagnostic and exit-status contract.

## Emitter hooks

The new emitter methods are:

- `runtimeMakeChannel(node, context)`
- `runtimeSend(node, context)`
- `runtimeReceive(node, context, commaOK)`
- `runtimeClose(node, context)`
- `runtimeSelect(node, context, bodyDispatcher)`
- `runtimeChannelRange(node, context, bodyDispatcher)`
- `runtimeGo(node, context, privateCallAdapter)`

The body dispatcher receives the same explicit context and must preserve source
mapping, control-flow targets and lexical scope. Channel range captures its
channel once and creates a new iteration binding for each successful receive.
The task call adapter supplies capture setup at the launch site and an invocation
against the child context/session. It must capture callee and argument values
once at scheduling time; the helper refuses a missing adapter.

A select with receive assignment to existing locations currently needs a
follow-up emitter case; bare receives, receive declarations and sends are
implemented. The compiler must distinguish channel range from collection range
using its type information. These helpers are not installed in the general
statement dispatcher by this change.

## Capability and external boundaries

`ChannelReference` creates an internal typed capability cell. Its owner and
value fields are private. `ResolveChannel` requires that actual cell, the
correct scope and the correct channel element type. Strings cannot reconstruct
authority; even the cell's display text is intentionally lossy. Scope closure
revokes all references and wakes blocked operations.

`CheckChannelBoundary` rejects channels and capability cells nested in interface,
map, slice, array, struct or pointer values. The shell/external bridge must call
it before stringification, environment export, subprocess arguments or a shell-
copy boundary. A check after conversion to a plain string cannot recover erased
provenance. Closures carrying channels also require callable capture metadata;
reflection alone cannot inspect their captured environment. Typed tasks share
an owner's scope, while shell-copy and new-file boundaries require rejection or
explicit provenance pruning. Registry tests do not certify unwired bridge paths.

Program cleanup must cancel and join owned tasks before revoking their channel
scope. Context cancellation and scope revocation are distinct errors, so reversing
that order can change the primary failure. `ChannelScope.Close` revokes authority;
it does not silently close all native channels.

## Verification and remaining parity work

Helper artifact tests parse and emit native channel operations from actual
Bash++ source, compile a binary, remove the generated source, and run the binary
with no shell or Go tool on PATH. Exact stdout, stderr and successful status are
compared with the interpreter. Cases exercise buffered/closed string channels,
comma-ok receives, selected/default arms, an unbuffered task producer and channel
range. These are helper artifact tests, not full compiler-dispatch certification.

Race-enabled runtime tests cover blocked send, receive and empty select
cancellation; launch arming; concurrent close/send and close/select; ready/default
selection; scope ownership and revocation; capability forgery and nested boundary
rejection; and pre-canceled operations that must not publish a value.

An observed parity gap remains explicit: after a closed `chan int` is drained,
native Go returns integer zero, but the current interpreter's `println` path
printed an empty string in the equivalent fixture. The runtime preserves native
integer zero. The compiler's shell-value projection and receive provenance need
an explicit resolution before numeric closed-channel output can be certified.
The string-channel fixture does not close that gap. Complete diagnostic wording,
file boundaries, select assignment forms, callable capture provenance and the
full concurrency corpus remain integration work.
