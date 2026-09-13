# Bash++ source-preserving front end — what closes the three by-ID rows (S165.5, design)

Status: **DESIGN NOTE, Sprint 165** (story #101, `54313fa7cf4d`). No product
code. Written from the code (`sh/gosource/{source,verdict,gctree,
diag_gcimage,convert}.go`, the vendored `sh/gosource/internal/gcsyntax`,
Go 1.27 `cmd/compile/internal/{syntax,types2,noder}`, `go/{parser,types}`),
from the upstream checker runners (`go/types/check_test.go`,
`cmd/compile/internal/types2/check_test.go`) and from run 0 of Sprint 165
re-measured on the leaf host (`leaf-165r0/`). The numbers in §2 were
counted against run 0's manifests and the typechecker suites run in full
through the patched runners on this lane's candidate.

## 0. Summary

- Bash++'s check interface is **gc's parser + go/types on go/parser's
  tree**. gc is **gc's parser + types2 on gc's own tree**. The two agree
  whenever both parsers build the same tree, which is every accepted
  program and — after Sprint 162's mirror (`mirrorGCTree`), this sprint's
  extension of it to the checker-test policy and the scanner image
  (`gcSourceImage`) — every program gc's parser rejects *without* a
  `syntax error`. They disagree only where **go/ast cannot carry what gc's
  tree carries** or where **go/parser's recovery from a syntax error differs
  from gc's**. That gap is the by-ID class "needs a source-preserving front
  end".
- The gap today is **7 roots (14 rows, 2 of them shared with §1.3)**: the
  three recorded by ID (`issue23586`, `issue4468`, `issue50372`: 6 rows,
  both modes), plus four typechecker roots this lane measured to the same
  cause — `types2 TestCheck/stmt0.go`, `types2 TestLocal/issue47996.go`,
  `types2 TestLocal/issue68183.go` (both modes each) and `go/types
  TestCheck/stmt0.go`, whose two missing rows are this class and whose
  `got col` row is §1.3's. Nothing else in run 0's 528 rows is this class
  (§2).
- **Route A** (vendor `types2` as the verdict checker over gc's own tree)
  closes all 7 and retires the mirror and the image; cost ≈ 25 k vendored
  lines (types2 + `internal/types/errors`), one more checker pass per
  load, and a re-measurement of the 371+372 typechecker fixtures whose
  go/types-positioned `ERROR` comments would now see types2's positions.
  **Route B** (a `gcsyntax` → `go/ast` bridge) closes only what go/ast can
  represent — issue47996, issue68183 and future recovery divergences — and
  **cannot** close the three by-ID rows or stmt0 without a residual layer
  that re-implements two types2 rules on gc's tree; cost ≈ 1 k lines of
  bridge plus that layer. The three go/types `got col` roots (§1.3) are
  closed by **neither**: they need go/parser's positions, which only
  go/parser has.
- Recommendation: **not this sprint**. The 7 roots are 1.3 % of the 528
  rows re-measured at run 0; Route A is the right long-term shape (it is
  what gc is) but its blast radius is the 743 typechecker rows that PASS
  today, and it needs its own barrier. Record the four typechecker roots by
  ID beside the three testdir roots, under the same decision, with this
  note as the reason.

## 1. The gap, by mechanism

### 1.1 Forms go/ast cannot carry (the three by-ID rows + stmt0's two)

| root | construct | gc | Bash++ |
|---|---|---|---|
| `fixedbugs/issue4468.go` (rows 22–23), `fixedbugs/issue23586.go`, `internal/types/testdata/check/stmt0.go:232,242` (both runners) | `go F`, `defer F`, `go 1;` — a go/defer whose operand is not a call | gc's parser keeps the operand (`CallStmt.Call` is any `Expr`); **types2** reports `expression in go must be function call` (stmt.go) | go/parser reports the same wording itself and sets `GoStmt.Call = nil` (`*ast.CallExpr` cannot hold a non-call); the gc verdict replaces go/parser's diagnostics, gc's parser has none for it, go/types sees `nil` and is silent (stmt.go `suspendedCall`: "error reported by parser"). The row is **missing**. |
| `fixedbugs/issue50372.go` | `for i, j, k = range s` — three or four range variables | gc's parser keeps the list (`RangeClause.Lhs` is an `Expr`, a `ListExpr`); **types2** reports `range clause permits at most two iteration variables` | go/parser reports `expected at most 2 expressions` and keeps two (`RangeStmt.Key/Value`); the verdict retains gc's parser rows (none) and go/types checks a two-variable range: **wording=4;extra=4**. |

The Sprint 162 mirror cannot help: there is no go/ast node to mirror gc's
into. Sprint 162 recorded the three testdir roots by ID for exactly this
reason (`gosource/testdata/sprint162/diag/FINDINGS.md`).

### 1.2 Recovery after a `syntax error` (two typechecker roots)

`types2 TestLocal/issue47996.go` — `func T [P] m () {}`: after this
sprint's parser-stream change the six syntax rows match; go/parser's
recovery leaves a reference to `P` that go/types reports (`undefined: P`)
while gc's recovered tree has none. `types2 TestLocal/issue68183.go` —
`☹x`: gc's scanner keeps the invalid character *inside the identifier*
(`atIdentChar` accepts any non-ASCII rune with an error), so `☹x` is one
name and types2's `isValidName` guard suppresses every follow-on; go/scanner
returns ILLEGAL and then `x`, so go/types sees `x` undefined, `T` undefined
and `fmt` unused. Both are tree-shape divergences after a *syntax-stage*
rejection; the scanner image of this sprint (`gcSourceImage`) covers the
byte-level cases gc's *source layer* drops (NUL, invalid UTF-8, BOM — nul1)
but not a scanner-level classification difference like `☹`.

### 1.3 Not this class: go/parser's positions (three go/types roots)

`go/types TestCheck/{decls2,expr3.go,stmt0.go}` fail with `got col = N;
want M` and nothing else after this sprint's mirror. The upstream go/types
runner parses with **go/parser** and matches columns with **tolerance 0**;
its shared testdata places each `ERROR` comment at go/parser's column
(`method has no receiver` at the `(`, col 6; gc's parser reports it at the
name, col 47 — types2's runner tolerates 50). Bash++'s syntax verdict is
gc's (Sprint 154 D3), so it reports gc's columns. Neither route in §3 changes
this: gc's tree has gc's positions. The only mechanism that would is a
runner-declared fact — "this runner's parser is go/parser" — under which
the check interface would report go/parser's own syntax diagnostics for
that runner; that makes the product's syntax verdict runner-dependent and
is a decision for the manager, not a front end. Recommend recording the
three by ID with that sentence.

## 2. The number — enumerated from run 0

Run 0 (`leaf-165r0/manifests`, 528 rows) was searched for every row whose
verdict class is `missing`, `wording`, `extra`, `multiplicity` or
`position`, and the typechecker suites were run in full through the patched
runners against this lane's candidate (with the harness's continuation
fold of the ledger's request applied): 367/371 go/types and 368/372 types2
fixtures pass.

| class | rows | roots | owner after this lane |
|---|---:|---:|---|
| go/defer non-call operand (§1.1) | 6 | 3 | `issue4468`, `issue23586` (by ID, unchanged); `types2 TestCheck/stmt0.go` (this note) |
| range variables (§1.1) | 2 | 1 | `issue50372` (by ID, unchanged) |
| recovery after a syntax error (§1.2) | 4 | 2 | `types2 TestLocal/issue47996.go`, `types2 TestLocal/issue68183.go` (this note) |
| go/parser's positions (§1.3) | 6 | 3 | `go/types TestCheck/{decls2,expr3.go,stmt0.go}` — **not** a front-end row |
| `nul1.go` (scanner image) | 2 | 1 | **fixed** this sprint (`gcSourceImage`) |
| `issue20298.go` `-e=0` error limit | 2 | 1 | gc's `base.ErrorfAt` limit ("too many errors" after ten) — a check-interface option the harness must pass from the recipe flag; owner 154 + harness, not a front end |
| `issue18459`, `issue18882` pragma position; `closure3`, `issue18895`, `issue19261`, `issue37837`, `issue42284`, `issue56280`, `linkname`, `nilptr3`, `prove` compiled `-m`/`-d` rows | 20 | 11 | lower (152, D11 (a)) — emitter fidelity, not a front end |

**A source-preserving front end closes 7 roots** (§1.1 + §1.2: 3 by ID +
`types2 stmt0.go` + `issue47996` + `issue68183` = 6 roots / 12 rows, plus
`go/types stmt0.go`, which needs §1.3 as well), of which 3 roots are the
recorded by-ID rows. It would also retire two mechanisms this sprint added as
approximations of it — `mirrorGCTree` (receiver forms, bad literals) and
`gcSourceImage` — and, with Route A, the go/types-vs-types2 wording
differences the Sprint 162 ledger noted as out of scope (`issue6572`'s "2nd
function result" family; none of those is a run-0 FAIL, so they count for
nothing today).

## 3. The two routes

### Route A — types2 over gc's tree (vendor `cmd/compile/internal/types2`)

**What it is.** The verdict becomes gc's exactly: `gcsyntax.Parse` (already
vendored, Sprint 154 D3) then `types2.Config.Check` on the same tree, with
`IgnoreBranchErrors`/`Error`/`Importer` configured as `noder/irgen.go`
configures them. go/types stays where it is needed for *conversion* (the
positioned Bash++ AST is built from `go/ast` + `types.Info` in
`convert.go`, 1.7 k lines) — an accepted program parses identically under
both parsers, so the converter never sees a divergent tree. A rejected
program never reaches the converter.

**What it closes.** All 7 roots of §2 (types2 reports the go/defer and
range rules on gc's tree; its recovery is gc's; `isValidName` is its own),
and every future divergence of this class, because there is no second
tree. `mirrorGCTree`, `gcSourceImage`, `checksAfterSyntaxVerdict`'s split
between the two trees and the checker-test policy's unfiltered-stream
switch collapse into "run types2 the way the runner runs it". The
types2 runner's rows then match by construction.

**What it costs.**

- Vendoring: `cmd/compile/internal/types2` is 69 files / 23,203 lines
  (Go 1.27.0), importing only `cmd/compile/internal/syntax` (vendored),
  `internal/types/errors` (1,998 lines), `internal/goversion` (one
  constant), `go/constant`, `go/token`, `go/version`. Same rule as
  `gcsyntax`: BSD-3, unmodified, `CREDITS`, never edited (the vendored-parser
  rule extends to the vendored checker). Two 25 k-line copies of the Go type
  checker in the module, kept at the same Go version as the toolchain the
  harness pins.
- An importer: types2 wants `types2.Importer` (its own `Package` type);
  gosource's importer map (`mapImporter`, explicit `Packages`, the
  identity-keyed visibility rule of S165.2) is written against go/types.
  Either the map is checked twice (once per checker: ≈ 2× check time per
  load; the 60 s bound is dominated by interpretation, not checking, but the
  typechecker roots run 743 checks each) or types2 imports through
  `importer.ForCompiler`'s export-data path, which does not see the
  in-memory map.
- The go/types runner: its 371 fixtures place `ERROR` comments at
  go/parser's/go/types' columns with tolerance 0. types2 positions differ
  for some constructs (the three §1.3 roots show the parser side; the
  checker side is unmeasured). Before Route A ships, the full typechecker
  suite must run through the patched runners on the candidate — the number
  of go/types fixtures that flip PASS → FAIL is the real cost, and it is
  unknown until measured. Everything in gc-stderr mode (the 3,000+ testdir
  rows) gets *closer* to gc, not farther.

**Risk to the 2,722 PASS.** Low for testdir (the verdict moves toward gc's
own checker); unknown and possibly material for the go/types half of the
typechecker family (up to 371 rows, both modes) — mitigated only by a
measurement, not by design.

### Route B — `gcsyntax` tree → `go/ast` bridge

**What it is.** A converter from `gcsyntax.Node` to `go/ast` nodes with
positions minted in a `token.FileSet` (the reverse of what the go/types ↔
types2 generator does inside the Go tree), so go/types checks gc's
recovered tree. `mirrorGCTree` is a two-rule version of this bridge; the
full bridge replaces it and go/parser entirely for the verdict.

**What it closes.** §1.2 (issue47996, issue68183) and every recovery
divergence after a syntax error, plus the `mirrorGCTree` cases. It does
**not** close §1.1: `ast.GoStmt.Call`/`ast.DeferStmt.Call` are
`*ast.CallExpr` and `ast.RangeStmt` has `Key`/`Value` only, so the bridge
cannot carry a non-call operand or a third range variable, and go/types
has no code path that reports them. Closing the three by-ID rows and stmt0
under Route B needs a residual layer that evaluates two types2 rules
(`expression in go/defer must be function call`, `range clause permits at
most two iteration variables`) on gc's tree before bridging — a
hand-maintained slice of types2, the shape this lane declined to start
(`gosource/testdata/sprint165/gosource-diag/FINDINGS.md`).

**What it costs.** ≈ 50 node kinds in `gcsyntax/nodes.go` → ≈ 1,000 lines
of bridge, plus the residual layer (≈ 100 lines, growing with every
unrepresentable form Go adds), plus comment/line-directive carriage for
`lineDirectives` and `validateCompilerDirectives`, which read `ast.File.
Comments`. No new vendoring. Conversion (`convert.go`) could keep
go/parser's tree, or move to the bridged tree — if it moves, the
`typedjson`/positions contract of the Bash++ AST inherits gc's position
model (line/col only; no byte offsets — `SourceAt` needs them).

**Risk to the 2,722 PASS.** Medium: the bridge is a second implementation
of go/parser's output for every accepted program; any node it builds
differently from go/parser changes what the converter sees for the 3,000+
rows that pass today. Mitigated by using it for the verdict only and
keeping go/parser's tree for conversion — which leaves two trees and the
residual layer, i.e. the present design with a bigger mirror.

### Route C — for the record: the go/types runner's parser fact

Neither route touches §1.3. Passing the go/types runner a runner-environment
option ("the runner's parser is go/parser") the way `--go-checker-branch-
errors` and `--go-check-after-syntax-errors` declare runner facts would let
the check interface return go/parser's syntax diagnostics (positions and
wording) for that runner only; 3 roots, ≈ 20 lines, but it makes the
product's syntax verdict depend on who asks, which D3 exists to prevent.
Manager's call; this note recommends by ID.

## 4. Recommendation and the record

1. **Do not build either route in Sprint 165.** The closable set is 7
   roots; Route A's cost is a barrier-scale measurement of the typechecker
   family; Route B does not close the by-ID rows without re-implementing
   types2 rules by hand.
2. **Record by ID, one decision, seven roots**: the three testdir roots
   already recorded (`issue23586`, `issue4468`, `issue50372`) and the four
   typechecker roots of §1.1–§1.2 (`types2 TestCheck/stmt0.go`,
   `types2 TestLocal/issue47996.go`, `types2 TestLocal/issue68183.go`,
   and `go/types TestCheck/stmt0.go`, whose two missing rows are §1.1 and
   whose `got col` row is §1.3), with this note as the reason.
3. **Record the three §1.3 roots** (`go/types TestCheck/decls2`,
   `expr3.go`, `stmt0.go`) by ID as "the go/types runner expects go/parser's
   columns at tolerance 0; the syntax verdict is gc's (D3)" — or take
   Route C as a declared runner fact.
4. **When a front end is scheduled**, take Route A, in its own sprint,
   with the full typechecker suite through the patched runners as the
   entry and exit measurement, and retire `mirrorGCTree`/`gcSourceImage`
   in the same change so there is one tree again.
