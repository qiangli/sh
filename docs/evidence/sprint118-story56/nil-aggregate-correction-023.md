# Nil aggregate correction review

Sprint: #118; Story: #56; Story-ID: 3ef468f4e831

This cumulative correction reviews issue-65 HEAD `e038ebef` on manager image
candidate `3d261ad4`, with package-channel prerequisite `1b1bbceb` applied.
It includes the nil aggregate implementation and ten authored controls from
that worker, without unrelated board edits or `fdf19b9c` methods-errors work.

Classic comparison keeps its existing printable interface payload. Only a
Go-source Runner recovers aggregate interface identity before comparison;
an internal runtime control exercises the same field expression in both modes.
Typed pointer-nil conversion recognition is also restricted to Go-source mode.

Nil function/channel fields carry declared types as nil bridge descriptors,
without creating live handles or channel capabilities. Native type resolution
supports function signatures, including variadic parameters; helper declarations
contain types only. Aggregate and alias readback compare their already evaluated
values, preserving index side effects exactly once. Range variables and native
interface transport retain static interface wrappers, so an interface containing
a typed nil function, channel, or pointer remains distinct from a nil interface.
Nonempty interface method checks remain required.

The focused race family contains 16 authored programs executed unchanged in
native, interpreted, and source-free compiled modes, plus classic runtime and
invalid-element controls. New controls cover function/channel fields, aliases,
variadic function types, indexed evaluation order, typed nil interface range
values, and nonempty interfaces holding nil pointers. No original bodies are
compiled into dependency helpers.

All ten original Go 1.27 errors test roots remain in the corpus gate; this slice
does not turn their broader failures into exclusions. The preceding replay was
3 PASS / 7 FAIL. Full final logs and exact verdicts are retained outside the
checkout at:
`~/.local/state/bashy/sprint118-evidence/nil-aggregate-review-023/`.

Raw worker reports and every failed review iteration are retained there too.
An authored `[]Box{b}` control encountered the separately known structured
variable-as-element failure; its raw failure remains recorded. The final alias
control constructs its own literal, isolating nil readback without claiming that
unrelated element-construction gap was fixed. An attempted classic source control
used syntax the parser rejects; the final classic control directly exercises the
runtime comparison with AST and stored values and preserves the old result.

This is a scoped correction, not a claim of full Go interface, callable, channel,
or standard-library corpus convergence.
