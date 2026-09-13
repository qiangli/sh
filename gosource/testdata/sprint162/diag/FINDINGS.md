# Sprint 162 diagnostics findings

| root | first cause | owner | status |
| --- | --- | --- | --- |
| testdir:fixedbugs/issue11362.go | import path canonicalization occurs after gcsyntax and before importer resolution | 162.5 here | fixed pending leaf evidence |
| testdir:import6.go | import path validation occurs after gcsyntax and before importer resolution | 162.5 here | fixed pending leaf evidence |
| testdir:fixedbugs/issue23586.go | gcsyntax accepts; go/types checks deferred expression | 162.5 here | existing gcsyntax verdict applies |
| testdir:fixedbugs/issue4468.go | gcsyntax rejects parenthesized go expression | 162.5 here | existing gcsyntax verdict applies |
| testdir:fixedbugs/issue50372.go | gcsyntax accepts; go/parser recovery must not suppress go/types range check | 162.5 here | existing gcsyntax verdict applies |
| testdir:slice3err.go | gcsyntax parser diagnostic | 162.5 here | existing gcsyntax verdict applies |
| testdir:nul1.go | gcsyntax scanner preserves supplied working-copy source name | 162.5 here | existing gcsyntax verdict applies |
| package:cmd/compile/internal/abt | testing/internal/testdeps internal import | 162.2 packages | moved to 162.2 |
| testdir:blank.go | lowered package-level blank-name collision | 162.1 interp | moved to 162.1 |
| testdir:convT2X.go | interface comparison representation | 162.1 interp | moved to 162.1 |
| testdir:defernil.go | deferred nil call execution | 162.1 interp | moved to 162.1 |
| testdir:fixedbugs/issue15002.go | collection bounds | 162.1 interp | moved to 162.1 |
| testdir:fixedbugs/issue18661.go | interface comparison representation | 162.1 interp | moved to 162.1 |
| testdir:fixedbugs/issue30606b.go | reflect bridge type assignment | 162.3 runtime | moved to 162.3 |
| testdir:fixedbugs/issue50672.go | method resolution on function value | 162.1 interp | moved to 162.1 |
| testdir:fixedbugs/issue5793.go | complex builtin arity | 162.1 interp | moved to 162.1 |
| testdir:fixedbugs/issue71675.go | recover call lifecycle | 162.1 interp | moved to 162.1 |
| testdir:fixedbugs/issue7740.go | native scalar bridge conversion | 162.3 runtime | moved to 162.3 |
| testdir:index0.go | runtime output/recipe execution | 162.3 runtime | moved to 162.3 |
| testdir:iota.go | iota constant evaluation | 162.1 interp | moved to 162.1 |
| testdir:literal2.go | floating constant evaluation | 162.1 interp | moved to 162.1 |
| testdir:nilptr.go | interpreter allocation growth | 162.3 runtime | design |
| testdir:range.go (interpreted) | range value evaluation | 162.1 interp | moved to 162.1 |
| testdir:range.go (compiled) | lowered parallel assignment | 162.4 lower | moved to 162.4 |
| testdir:typeparam/equal.go | interface comparison representation | 162.1 interp | moved to 162.1 |
| testdir:typeparam/issue50690a.go | multi-result expression conversion | 162.1 interp | moved to 162.1 |
| testdir:typeparam/issue54302.go | interface comparison representation | 162.1 interp | moved to 162.1 |
| testdir:typeparam/mdempsky/13.go | generic bridge argument evaluation | 162.3 runtime | moved to 162.3 |
| testdir:typeparam/subdict.go | comparable type constraint | 162.1 interp | moved to 162.1 |
| testdir:typeswitch1.go | type-switch/default-value evaluation | 162.1 interp | moved to 162.1 |
| testdir:zerosize.go | zero-size pointer identity | 162.1 interp | moved to 162.1 |
| typechecker:go/types/TestCheck/decls2 | checker diagnostic column mapping | 162.1 interp | moved to 162.1 |
| typechecker:go/types/TestCheck/expr3.go | checker diagnostic column mapping | 162.1 interp | moved to 162.1 |
| typechecker:go/types/TestCheck/stmt0.go | checker diagnostic column mapping | 162.1 interp | moved to 162.1 |

## Requests to other seams

None. Manifest movement is owned by the harness lane; the assignments above are its requested destinations.
