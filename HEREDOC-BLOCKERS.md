# heredoc fixture — remaining diff blocked on out-of-scope directories

Status of `external/bash-5.3/tests/heredoc.tests` vs `heredoc.right` on this
branch (`agent/weave-issue-4`): **3 differing lines** (6-line diff output).
Both remaining clusters are fully diagnosed and have **verified fixes**, but
the fixes live in `cmd/bashy/` and `interp/`, which this agent's scope
explicitly forbids modifying (allowed: `syntax/` and the Makefile
`test-bash-helpers` target only). The `syntax/` side needed for both clusters
is already complete and committed; nothing further is required there.

Repro (from repo root):

```sh
make build && make test-bash-helpers && cd external/bash-5.3/tests && \
  BASHY=$(realpath ../../../bin/bashy) && \
  THIS_SH=$BASHY BUILD_DIR=$PWD/.. PATH=$PWD:/usr/bin:/bin:/usr/local/bin \
  $BASHY ./heredoc.tests 2>&1 | diff - ./heredoc.right
```

Current diff:

```
126a127
> ./heredoc7.sub: line 17: warning: command substitution: 1 unterminated here-document
139d139
<
151d150
<
```

**Verified:** with the patch at the bottom of this file applied, the diff is
**empty** (measured 2026-06-10, this exact tree). No regressions: `cprint`,
`func`, `exportfunc`, `comsub-eof`, `heredoc` fixtures all PASS with the
patch; `type` fails identically (byte-for-byte same 10-line diff) before and
after, i.e. pre-existing. `go build ./...` and `go test ./syntax/...` green.

## Cluster 1 — missing comsub unterminated-heredoc warning (cmd/bashy)

`heredoc7.sub` line 17 is `echo $(cat << EOF)` — a heredoc opened inside a
command substitution whose body is never read before the closing paren. Bash
warns at parse time:

```
./heredoc7.sub: line 17: warning: command substitution: 1 unterminated here-document
```

The parser-side hook **already exists and fires correctly**:
`syntax.HeredocComsubWarning` (`syntax/parser.go:249`, fired from
`cmdSubst()` at `syntax/parser.go:1554-1557`, covered by
`syntax/parser_test.go:213` `TestParseHeredocComsubWarning`). Run over the
full `heredoc7.sub` it reports exactly `line=17 count=1` plus the (already
wired) EOF warning at line 29 — matching bash precisely.

The only missing piece: `cmd/bashy/main.go` never registers the option. It
registers `syntax.HeredocEOFWarning` in three parser-construction sites but
not `HeredocComsubWarning`. Note that scripts containing both `$(` and `<<`
(like heredoc7.sub) take the `runStatementStream` path
(`needsStatementStreamRecovery`, main.go ~1388), **not** `parseOnce` — the
streaming path buffers warnings and flushes them before running each
statement, so the comsub warning must be buffered the same way to land ahead
of the statement's stdout (`foo bar`). Wiring only `parseOnce` is not enough
(verified: warning still missing with only that site patched).

Message format (matches bash 5.3 parse.y): plural `s` suffix when count > 1:
`%s: line %d: warning: command substitution: %d unterminated here-document%s`.

## Cluster 2 — stray blank line after heredoc terminator before `then`/`do` (interp)

`heredoc9.sub` defines functions whose `if`/`while` *condition* carries a
heredoc, then runs `declare -pf foo`. Bash prints the terminator adjacent to
the following keyword:

```
    if cat <<HERE
contents
HERE
    then
```

bashy inserts a blank line between `HERE` and `then` (and `HERE` and `do`).
The blank is added by `bashDeclareFmt` in `interp/runner.go` (~line 3274):
after each heredoc terminator it inserts a blank line — bash 5.3's
between-statements convention — with an existing exception for a following
`)`. The exception must also cover a following `then`/`do`: there the heredoc
fed the compound's condition, and bash keeps terminator and keyword adjacent.
(The plain `syntax.NewPrinter` output is fine; this is purely the
`declare -f` reformatting helper.)

Note `interp/vars.go` `bashExportedFuncValue` (~line 225) has a similar
blank-after-terminator insertion for exported functions; the fixture corpus
doesn't exercise the if/while-condition case there, so it was left alone, but
the same rule likely applies.

## Verified patch (apply to make the heredoc diff empty)

```diff
diff --git a/cmd/bashy/main.go b/cmd/bashy/main.go
--- a/cmd/bashy/main.go
+++ b/cmd/bashy/main.go
@@ -1203,6 +1203,15 @@ func run(r *interp.Runner, reader io.Reader, name string) error {
 			"%s: line %d: warning: here-document at line %d delimited by end-of-file (wanted `%s')\n",
 			errPrefix, eofLine, startLine, stop)
 	}
+	comsubWarn := func(line, count int) {
+		plural := ""
+		if count > 1 {
+			plural = "s"
+		}
+		fmt.Fprintf(os.Stderr,
+			"%s: line %d: warning: command substitution: %d unterminated here-document%s\n",
+			errPrefix, line, count, plural)
+	}
 	ctx := context.Background()
 	r.Reset()
 	if err := interp.WithBashSource(src)(r); err != nil {
@@ -1224,7 +1233,8 @@ func run(r *interp.Runner, reader io.Reader, name string) error {
 	// r.Run(prog) would. The -c case (`*command != ""`) skips
 	// recovery; bash also fails -c entirely on parse error.
 	parseOnce := func(chunk []byte, parseLang syntax.LangVariant) (*syntax.File, syntax.ParseError, bool) {
-		f, perr := syntax.NewParser(syntax.Variant(parseLang), syntax.HeredocEOFWarning(hdocWarn)).
+		f, perr := syntax.NewParser(syntax.Variant(parseLang), syntax.HeredocEOFWarning(hdocWarn),
+			syntax.HeredocComsubWarning(comsubWarn)).
 			Parse(bytes.NewReader(chunk), name)
 		if perr == nil {
 			return f, syntax.ParseError{}, false
@@ -1413,7 +1423,25 @@ func runStatementStream(
 	hdocWarn := func(startLine, eofLine int, stop string) {
 		hdocWarnings = append(hdocWarnings, hdocWarning{startLine, eofLine, stop})
 	}
+	type comsubWarning struct {
+		line  int
+		count int
+	}
+	var comsubWarnings []comsubWarning
+	comsubWarn := func(line, count int) {
+		comsubWarnings = append(comsubWarnings, comsubWarning{line, count})
+	}
 	flushWarnings := func() {
+		for _, warning := range comsubWarnings {
+			plural := ""
+			if warning.count > 1 {
+				plural = "s"
+			}
+			fmt.Fprintf(os.Stderr,
+				"%s: line %d: warning: command substitution: %d unterminated here-document%s\n",
+				errPrefix, warning.line, warning.count, plural)
+		}
+		comsubWarnings = comsubWarnings[:0]
 		for _, warning := range hdocWarnings {
 			fmt.Fprintf(os.Stderr,
 				"%s: line %d: warning: here-document at line %d delimited by end-of-file (wanted `%s')\n",
@@ -1425,7 +1453,8 @@ func runStatementStream(
 	cursor := 0
 	for cursor < len(src) {
 		parseLang := r.LangVariant()
-		parser := syntax.NewParser(syntax.Variant(parseLang), syntax.HeredocEOFWarning(hdocWarn))
+		parser := syntax.NewParser(syntax.Variant(parseLang), syntax.HeredocEOFWarning(hdocWarn),
+			syntax.HeredocComsubWarning(comsubWarn))
 		restart := false
 		chunk := paddedChunk(src, cursor, len(src))
 		for stmt, err := range parser.StmtsSeq(bytes.NewReader(chunk)) {
diff --git a/interp/runner.go b/interp/runner.go
--- a/interp/runner.go
+++ b/interp/runner.go
@@ -3280,6 +3280,12 @@ func bashDeclareFmt(body string, lastTop bool) string {
 				if strings.HasPrefix(nxtTrim, ")") {
 					continue
 				}
+				// bash 5.3 keeps the terminator adjacent to a
+				// following `then`/`do` (heredoc fed the if/while
+				// condition); the blank only separates statements.
+				if nxtTrim == "then" || nxtTrim == "do" {
+					continue
+				}
 				out = append(out, "")
 			}
 		}
```
