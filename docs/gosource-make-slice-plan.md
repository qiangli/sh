# Recoverable Go slice-allocation faults

Keep make on existing typed builtin cells. Decode integer size operands without
string coercion: an out-of-int-range integer is a dynamic size fault, while
invalid constant sizes remain checker errors. Evaluate both operands before
validation. Raise the existing runtime.errorString payload at the make fault
site, with native error text; never classify user panic strings by spelling.

Validate source element byte size before allocating interpreter carriers.
Use the existing go/types layout model, including recursive imported field/array-element type information;
an unknown or oversized layout is an explicit evaluator limitation. The
address-space ceiling follows Go 1.27 runtime/malloc.go heapAddrBits/maxAlloc
for the running target, not an arbitrary memory budget. Check negative length
and length greater than capacity first; source byte overflow uses length
before capacity. Allocation multiplication uses division to avoid overflow.

Only the two carrier allocations have a narrow makeslice-runtime-panic guard.
A source-valid allocation too large for dense []any storage is an explicit
representation refusal, not a fabricated runtime.Error. In particular, a huge
zero-sized-element slice remains legal natively but unsupported by dense
interpreter storage. Ordinary zero-sized and named slices remain supported.
Fatal process out-of-memory is not claimed recoverable.

Independent differential probes cover recovered dynamic types/text, signed
and unsigned overflow, byte/int and large-array element layouts, nested
append/copy, operand count/order, no post-fault execution, indirect recover,
constant rejection, successful spare-capacity zeros and user panic strings.
An explicit negative test records the huge-zero-size representation limit.

A separate mixed-width diagnostic (int length call, uint64 capacity call)
observed capacity then length on the local Go 1.27 native compiler and length
then capacity in the interpreter. That discrepancy is retained outside this
bounded fault repair; no compiler-bug cause is asserted. The source is retained in
`interp/testdata/gosource-make-slice/mixed_width_evaluation.go.txt`; the failed
differential receipt is retained separately for delivery accounting. The passing same-width
order control does not constitute repair of the mixed-width discrepancy.
