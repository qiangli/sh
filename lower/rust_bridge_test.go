//go:build full

package lower_test

import "testing"

// The call_with_one and do_twice inputs/assertions are the Fn/FnMut examples
// in rust-lang/rust 59807616e1fa2540724bfbac14d7976d7e4a3860,
// library/core/src/ops/function.rs (MIT OR Apache-2.0). The harness replaces
// Rust closures with Bash# closures; the original 1 -> 2 and 1+2+2 -> 5
// assertions are unchanged. Additional cases exercise the bridge contract.
func TestRustHandlesCallbacksInterpretedNativeParity(t *testing.T) {
	requireRustToolchain(t)
	got := testPythonFenceInterpretedNativeParityAt(t, `~~~rust as rs
use bashpp::{Callback, Handle};
use serde_json::json;
pub struct Res { pub n: i64 }
pub fn open(n: i64) -> Handle<Res> { Handle::new(Res { n }) }
pub fn read(h: Handle<Res>) -> Result<i64, String> { h.with(|r| r.n) }
pub fn call_with_one(func: Callback) -> Result<i64, String> { func.call_as(vec![json!(1)]) }
pub fn do_twice(func: Callback) -> Result<(), String> { func.call(vec![])?; func.call(vec![])?; Ok(()) }
pub fn ping(func: Callback, n: i64) -> Result<i64, String> { func.call_as(vec![json!(n)]) }
~~~
func double(x int) int { return x * 2 }
func pong(n int) int {
  if n >= 3 { return n }
  m := n + 1
  v := rs.ping(pong, m)
  return v
}
a := rs.call_with_one(double)
x := 1
addTwo := func() { x = x + 2 }
rs.do_twice(addTwo)
echo "$a,$x"
h := rs.open(7)
n := rs.read(h)
rs.release(h)
v, err := rs.read(h)
echo "$n,$v,$err"
r := rs.ping(pong, 0)
echo "reentry=$r"
`, "input.bpp")
	want := "2,5\n7,0,RUST-ECALL: Rust function read argument 1: stale Rust handle 1: released or never created\nreentry=3\n"
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
