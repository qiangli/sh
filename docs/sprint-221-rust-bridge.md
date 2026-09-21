# Rust handles and shell callbacks (Sprint 221, story 577)

The existing value adapter carries worker-owned `bashpp::Handle<T>` tokens;
the existing invocation protocol serves `bashpp::Callback` requests in strict
last-in-first-out order. There is no callback multiplexing. A callback must
finish before its invoking foreign call can finish, and recursive callbacks
are bounded at eight. Go embedders must pass the supplied callback context
to nested calls and must not use it concurrently from other goroutines.
Callbacks must observe cancellation; arbitrary host code cannot be forcibly
stopped. Bash# callbacks use the runner's cancellable execution context.

```bash
~~~rust as rs
use bashpp::{Handle, Callback};
use serde_json::json;
pub struct Resource { n: i64 }
pub fn open(n: i64) -> Handle<Resource> { Handle::new(Resource { n }) }
pub fn read(h: Handle<Resource>) -> Result<i64, String> { h.with(|r| r.n) }
pub fn apply(f: Callback) -> Result<i64, String> { f.call_as(vec![json!(1)]) }
~~~
func double(x int) int { return x * 2 }
h := rs.open(7)
v := rs.read(h)
x := rs.apply(double)
echo "$v,$x"
rs.release(h)
```

Handles belong to their originating module and worker generation. Release
runs Drop once; duplicate release and subsequent use fail. Worker death or
restart invalidates all old handles. Shared and mutable borrows enforce Rust
dynamic borrowing across re-entry without panicking. Releasing a borrowed
token invalidates it immediately; its value drops when the last borrow ends.
Callbacks expire when the call that passed them returns, even if retained by
the worker. Errors and panics return through the existing foreign envelope.

Validation uses `polyglot/rust_bridge_test.go`,
`interp/bashpp_rust_bridge_test.go`, and `lower/rust_bridge_test.go`.
The latter keeps the exact Fn/FnMut example inputs and expected values from
Rust commit `59807616e1fa2540724bfbac14d7976d7e4a3860`,
`library/core/src/ops/function.rs`; the interpreter and compiled program run
the same source. Resource lifecycle/borrow fixtures adapt Rust `rc.rs` and
`cell.rs` to handle operations, with locations and licenses in the test header.
Serde nested-newtype cases use commit
`de8500740cdcabffb9734f503e4889def823cf10` (`v1.0.151`).

Completion order: resource ownership and release; protocol callbacks and
bounded re-entry; interpreter/lowered bindings; race/cancellation and parity
gates. Cancellation terminates only process state while an exchange is active,
joins that exchange, then clears callback-owned pending output. Failed argument
encoding also expires callbacks allocated before the failing argument.
