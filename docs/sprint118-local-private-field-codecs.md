# Sprint 118: local private-field transport

Story #54, `c3a60493cde9`.

The dependency helper now materializes local struct values with generated typed
field codecs. Codecs may legally read and write private fields because the
mirrored declaration and codec share the helper package. Original method bodies
remain interpreted. There is no unsafe access or original program forwarding.

Codecs preserve declaration order, field names, tags, nested anonymous structs,
and defined struct identities. Parsed type keys reconcile harmless whitespace
without removing text from tags. Callback pointer receivers retain the existing
Origin association and write back through original interpreter storage.

SDK values inside fields remain native handles. Values arriving on a callback's
authenticated connection acquire that session's identity recursively; normal
request validation rejects stale handles after Reset. SDK private fields are not
read into a fabricated struct. Reference-bearing value receiver callbacks and
other unsupported transport operations retain their explicit refusal paths.

Validation uses unchanged bytes for each native Go, interpreter and separately
compiled artifact comparison. The artifacts run after generated and original
source files have been removed from their build directories. Eight cases cover
private values, pointer receiver effects/aliases, nested named and anonymous
structs, tag identity, empty/defined structs, and native value/pointer fields.
Additional checks exercise cancellation/Reset and a real stale native field
captured during an original callback. The prior private-field rejection fixture
now compares its original source with native Go instead of expecting rejection.

The full unchanged official 64bit.go generator advances beyond the private-field
transport refusal but still fails in the interpreter at line 103:19:
`BASHPP-ESELECTOR-ROOT: b is not a structured value`. The native and compiled
generators agree; the complete retained 1,340,126-byte generated child succeeds
in all three modes. This is partial progress, not completion of the generator.

Other boundaries remain separate work: imported function calls directly inside
some composite literals, local type aliases/helper-name collisions, and builtin
calls in return statements. No fixture rewrite or result normalization was used.

Focused callback/codec/lifecycle race checks passed in 143.899s; existing local
type regressions passed in 35.893s. Durable raw evidence is under
`~/.local/state/bashy/sprint118-evidence/local-codecs-013/manifest.json`.
