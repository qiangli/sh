# GoSource conversion capacity — Sprint 118, Story 52

This change repairs string-to-slice backing storage and preserves an original
constant-string operand in the typed AST. It does not establish complete
capacity parity with the pinned Go compiler. Classic Bash++ keeps its existing
conversion allocation behavior.

`[]byte` conversion of an original string constant uses exact-length storage.
The converter obtains that distinction from `go/types`, including named
constants and constant expressions; equal runtime string contents do not imply
constant syntax. Typed JSON preserves the distinction. For dynamic byte and
rune conversions, a fixed native primitive conversion determines the escaping
heap allocation capacity. These helpers receive string data, never original
program expressions or bodies. This replaces the rejected append-growth proxy:
append and conversion have different allocation paths.

The interpreter allocates its own typed element and metadata backing arrays to
that capacity, zeroes only the fresh spare storage, and retains ordinary slice
aliasing. Re-slicing and native `Read` writeback therefore see the same backing
array. Identity slice conversions retain their original backing array.

The implementation was checked against Go 1.27.0 darwin/arm64. Relevant SDK
implementation sources are `src/cmd/compile/internal/walk/convert.go`
(`walkStringToBytes`, `walkStringToRunes`) and `src/runtime/string.go`
(`stringtoslicebyte`, `stringtoslicerune`, `rawbyteslice`, `rawruneslice`).
Constant byte conversion lowers to an exact-length array. Dynamic conversions
can use compiler-reserved stack storage or runtime heap storage; byte and rune
allocation sizes differ. The language-level contents and fresh backing storage
do not imply one universal allocation capacity. Sprint corpus comparison still
requires equality with its pinned native oracle, so observed differences remain
failures.

## Validation and remaining failures

The committed differential controls execute each identical original program
with the real SDK, the GoSource Runner, and a real lowered native artifact after
its source has been removed. They cover byte/rune heap capacities, empty and
Unicode inputs, original constant versus equal dynamic operands, aliases,
append detachment, spare zero values, native `Read` backing updates, and
function/method/generic/closure result `len` and `cap` with operand side effects.
Internal checks compare real conversion capacities for lengths 0 through 129.
The constant metadata also has a typed-JSON execution round trip.

The existing tagged collection audit is unchanged. Its complete run on this
slice passed 12 of 17 cases and failed five: compiler-dependent append capacity,
unsupported native retention/mutation for `bytes.ToUpper` and `sort.Strings`,
and native-oracle panic rendering for nil map writes and nil slice indexing.
Those failures are not exclusions or accepted equivalences.

Two additional unchanged authored programs retain strict three-mode failures:

```go
package main
import "fmt"
func text() string { return "abc" }
func main() { b := []byte(text()); fmt.Println(len(b), cap(b)) }
```

The native and lowered outputs are `3 3`; the interpreter output is `3 8`.
Original type-checker constant metadata cannot describe every later compiler
optimization. Replacing `[]byte` with `[]rune` produces native/lowered `3 32`
and interpreter `3 4`, exposing compiler-reserved stack capacity. The existing
append-growth audit similarly observes initial native capacities 4/2/32 for
int/string/bool slices versus interpreter heap capacities 1/1/8. No minimum
capacity guess, source-body compilation, or comparator relaxation is applied.

Raw race, frontend, full tagged-audit JSON, and three-mode residual records are
retained in the worker's `.agents/review/capacity-*` evidence. These results
support the bounded backing-storage repair; they do not close the parent
collection or full-corpus acceptance requirements.
