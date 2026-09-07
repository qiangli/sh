# Directional channel signatures

Committed Bash++ function signatures accept `chan T`, `chan<- T`, and
`<-chan T` parameters and results. The parser retains a positioned
`BashPPChanType` implementing `BashPPTypeExpr`, with the arrow position,
explicit direction, and structured element type. The legacy `Elem` spelling
remains available for existing `make(chan T)` consumers. `Walk` visits
`Element` when present, otherwise `Elem`, so it does not visit duplicate type
representations. Printing and typed JSON retain the directed type.

A bidirectional channel can be passed to a matching directed parameter. A
restricted channel cannot be passed to the opposite direction or widened back
to bidirectional. The call checks real channel metadata and element identity;
rendering a channel handle as a string does not grant channel authority.
Receive-only bindings reject send and close; send-only bindings reject receive,
including select and range operations. These checks retain the existing task
group and shell-copy capability boundaries.

Channel payload rendering remains the established interpreter contract. A
closed channel receive produces an empty rendered value with `ok == false`,
including an `int` channel. This change does not replace that source observable
with native Go's numeric zero. Compiled parity must compare the exact source
bytes and channel authority behavior separately from native Go type checking.

Verification lives in `syntax/bashpp_directional_test.go`,
`syntax/typedjson/bashpp_directional_test.go`, and
`interp/bashpp_directional_test.go`. The parser checks bytewise input, exact
positions, print/parse stability, and classic/POSIX redirection ownership.
