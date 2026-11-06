# S562 B3 imported package variables

The fresh candidate rejects the E1 recipe reproduction `args := os.Args`
with unused-import and package-not-in-selector diagnostics. Lowering treats
the package as a value and emits `(os).Args`; the Go-source-only shortcut
does not cover Bash# input.

Recognize package qualifiers for both input paths, while checking lexical
storage before choosing the shortcut. Function parameters and locals, nested
script scopes and script declarations must continue through ordinary value
selector handling, including pointer checks. Imports occupy the outer script
scope; function emission starts with fresh parameter/local scopes.

Keep an exact reproduction and an executable regression with an aliased
package, a same-name pointer local and a same-name parameter. Check byte-exact
output with a spaced process argument. Preserve the fresh-candidate failure
and separately stage unmodified-base and repaired-source tests for manager
verification on the claimed test host. No runner assertions or coverage change.

Manager verification: both baseline regressions fail with package-selector
diagnostics; both repaired regressions pass, including the executable artifact.
Existing imported-function, Go-source embedded-selector and per-file import
identity checks also pass. No tests are skipped.

The fixture uses `new(Holder)` and a bound temporary before returning a field:
the frontend currently rejects an address-of-composite short declaration and
a direct field-selector return in this Bash# form. Those separate restrictions
are outside this qualifier repair; original failed attempts remain in evidence.
