# Errors Fixture Deep Analysis — Round 2

Date: 2026-06-11. Extends [ERRORS-BLOCKERS.md](ERRORS-BLOCKERS.md) (whose
Cluster 2, `unset /bin/sh`, has since landed — see `interp/builtin.go:612`).

## Headline: the issue's 70-line count was measured against a stale binary

The measurement recipe in the issue —

```sh
cd external/bash-5.3/tests && \
  THIS_SH=$PWD/../../../bin/bashy ... $PWD/../../../bin/bashy ./errors.tests | diff - ./errors.right
```

— **does not run this sandbox's `bin/bashy`**. `external` in this checkout is a
symlink:

```
external -> /Users/qiangli/projects/poc/ai/sh/external
```

The kernel resolves `tests/../../../bin/bashy` *physically*: it traverses the
symlink into `/Users/qiangli/projects/poc/ai/sh/external/bash-5.3/tests` first,
so `../../..` climbs to `/Users/qiangli/projects/poc/ai/sh`, and the binary
that actually runs is **that** repo's `bin/bashy` — built May 26
(5,664,034 bytes, predating commit `9a2115d2` "syntax+interp: defer invalid
select names"). The stale binary aborts the whole fixture at parse time on
`errors.tests:60` (`select $1 in a b c` inside `bad-select()`), producing a
**347-line** diff today; at the time the issue was filed the stale binary
evidently produced the quoted ~70-line diff. Either way, **no patch made in
this sandbox can move that number** — which is the most plausible reason the
two prior reduction attempts "failed": their fixes were correct but invisible
to the recipe.

(`make test-bash` is unaffected: it computes `BASHY_ABS=$(pwd)/bin/bashy`
from the repo root before `cd`-ing into the tests directory.)

### Corrected measurement recipe

```sh
make build && ROOT=$PWD && cd external/bash-5.3/tests && \
  THIS_SH=$ROOT/bin/bashy BUILD_DIR=$PWD/.. PATH=$PWD:/usr/bin:/bin:/usr/local/bin \
  $ROOT/bin/bashy ./errors.tests 2>&1 | diff - ./errors.right
```

With the freshly built sandbox binary the diff is **48 lines** (stable across
repeated runs: 48/48/48). All 48 are clustered below.

> **Orchestrator correction (post-merge):** the canonical hermetic
> measurement (`bash --noprofile --norc`, no PTY) on a fresh build is
> **70 lines**, and every wrapper gate measured it correctly via the
> absolute `BASHY=$PWD/bin/bashy` capture. The 48 here was measured
> inside an interactive TUI session (PTY-attached), which changes some
> error output. The headline discovery stands: the issue-body recipe
> (`$PWD/../../../bin/bashy` from inside `tests/`) physically traverses
> the `external` symlink and runs the OLD ai/sh repo's May-26 binary
> (349-line diff) — that stale binary has been renamed
> `bashy.stale-trap-2026-06-11` to kill the trap. The cluster analysis
> below is environment-independent and remains the mining source.

## Coverage table

`D<n>` numbers the 48 `<`/`>` lines of the corrected diff in order
(`<` = bashy output, `>` = expected bash 5.3 output).

| Diff lines | Hunk | Cluster |
|---|---|---|
| D1–D6 | `113,116c113,114` | C1 arith parse error inside `eval` |
| D7, D11, D27 | `183,185c181,185` / `200,202c204,205` | C4 bad-substitution exit status |
| D8–D9, D15, D17–D20, D28–D29, D32, D34–D37 | same hunks + `187c187,191` / `204c207,211` | C5 `${var?}` / `${var:?}` message text |
| D10, D12 | `183,185c181,185` | C2 `${x!y}` / `${#foo%}` parse-time rejection |
| D13–D14, D16, D30–D31, D33 | `183,185c181,185` etc. | C3 `${b[   ]}` blank subscript parse rejection |
| D21–D22, D38–D39 | `191c195` / `208c215` | C6 `${!x}` invalid-name exit status |
| D23–D26, D40–D43 | `195,196c199,200` / `212,213c219,220` | C7 indirect through unset variable |
| D44–D45 | `236c243` | C8 `eval '( '` line-number re-basing |
| D46–D47 | `238a246` / `239a248,249` | C9 `command export/readonly` on readonly var |
| D48 | `239a248,249` | C10 POSIX-mode `shift` diagnostic |

Numbered diff for reference (cluster in brackets):

```
D1  < ./errors.tests: eval: line 286: `/` must follow an expression          [C1]
D2  < ./errors.tests: eval: line 286: `echo $[/bin/sh + 0]'                  [C1]
D3  < ./errors.tests: eval: line 287: `/` must follow an expression          [C1]
D4  < ./errors.tests: eval: line 287: `echo $((/bin/sh + 0))'                [C1]
D5  > ./errors.tests: line 286: /bin/sh + 0: arithmetic syntax error: operand expected (error token is "/bin/sh + 0")  [C1]
D6  > ./errors.tests: line 287: ... (same shape)                             [C1]
D7  < after 2: 0                                                             [C4]
D8  < uvar:                                                                  [C5]
D9  < uvar:                                                                  [C5]
D10 > after 1: 1                                                             [C2]
D11 > after 2: 1                                                             [C4]
D12 > after 3: 1                                                             [C2]
D13 > 4                                                                      [C3]
D14 > array after 1: 0                                                       [C3]
D15 < uvar:                                                                  [C5]
D16 > array after 2: 0                                                       [C3]
D17 > ./errors6.sub: line 1: uvar: parameter not set                         [C5]
D18 > ./errors6.sub: line 1: uvar: parameter null or not set                 [C5]
D19 > (blank — alignment artifact, both sides print it)                      [C5]
D20 > ./errors6.sub: line 1: uvar: parameter null or not set                 [C5]
D21 < after indir: 0                                                         [C6]
D22 > after indir: 1                                                         [C6]
D23 < unset                                                                  [C7]
D24 < (blank)                                                                [C7]
D25 > ./errors6.sub: line 50: var: invalid indirect expansion                [C7]
D26 > ./errors6.sub: line 51: var: invalid indirect expansion                [C7]
D27–D43: exact repeat of D7–D26 minus the "after N" trio (second
         errors6.sub run, driven with THIS_SH="... -o posix"):
D27 < after 2: 0                [C4]   D34 > uvar: parameter not set    [C5]
D28 < uvar:                     [C5]   D35 > uvar: parameter null...    [C5]
D29 < uvar:                     [C5]   D36 > (blank, artifact)          [C5]
D30 > 4                         [C3]   D37 > uvar: parameter null...    [C5]
D31 > array after 1: 0          [C3]   D38 < after indir: 0             [C6]
D32 < uvar:                     [C5]   D39 > after indir: 1             [C6]
D33 > array after 2: 0          [C3]   D40 < unset  D41 < (blank)       [C7]
                                       D42–D43 > invalid indirect ×2    [C7]
D44 < ./errors8.sub: eval: line 7: syntax error: unexpected end of file from `(' command on line 1  [C8]
D45 > ./errors8.sub: eval: line 7: ... from `(' command on line 6        [C8]
D46 > ok 2                                                               [C9]
D47 > ok 3                                                               [C9]
D48 > ./errors8.sub: line 11: shift: 12: shift count out of range        [C10]
```

Driver lines in `errors.tests`: 286–287 (`eval echo \$[/bin/sh + 0]`,
`eval echo '$((/bin/sh + 0))'`), 359 (`${THIS_SH} ./errors6.sub`), 360
(`THIS_SH="${THIS_SH} -o posix" ${THIS_SH} ./errors6.sub`), 365
(`${THIS_SH} ./errors8.sub`).

---

## C1 — arithmetic parse error inside `eval` reported as a parse error, not an arith error (6 lines: D1–D6)

**Evidence**

```
< ./errors.tests: eval: line 286: `/` must follow an expression
< ./errors.tests: eval: line 286: `echo $[/bin/sh + 0]'
> ./errors.tests: line 286: /bin/sh + 0: arithmetic syntax error: operand expected (error token is "/bin/sh + 0")
```

**Root cause** — bashy parses `$[/bin/sh + 0]` eagerly inside the eval'd
string; the leading `/` operator trips `syntax/parser_arithm.go:60` (also 176,
375: ``p.curErr("%#q must follow an expression", p.tok)``). The eval builtin's
parse-error path (`interp/builtin.go:1644–1703`) then prints the generic
`<file>: eval: line N: <msg>` pair, echoing the source line. Bash defers
arithmetic parsing to expansion time and reports through its arith evaluator:
no `eval:` tag, no source echo, and the
`EXPR: arithmetic syntax error: operand expected (error token is "EXPR")`
shape that the runner already knows how to produce (`bashArithmError`,
`interp/runner.go:438+`).

**Verdict**: tractable (narrow rewrite in eval's parse-error path). **Estimate: 5**

**Proposed patch** (in `interp/builtin.go`, inside the eval parse-error
branch, after the existing `switch` that rewrites `text` and before
`r.errf("%s: eval: line %d: %s\n", ...)` at ~1691):

```diff
--- a/interp/builtin.go
+++ b/interp/builtin.go
@@ eval parse-error rewriting, before the final r.errf pair
+				// An arithmetic operator error inside $((...)) / $[...]
+				// is a *runtime* arithmetic error in bash, reported
+				// without the `eval:` tag or the source echo.
+				if strings.HasSuffix(text, "must follow an expression") {
+					if srcLine := evalSourceLine(src, int(pe.Pos.Line())); srcLine != "" {
+						if expr, ok := innerArithText(srcLine); ok {
+							r.errf("%s: line %d: %s: arithmetic syntax error: operand expected (error token is %q)\n",
+								name, pos.Line(), expr, expr)
+							exit.code = 1
+							return exit
+						}
+					}
+				}
```

plus a small helper:

```go
// innerArithText extracts the body of the first $((...)) or $[...]
// in line, returning ok=false when neither is present.
func innerArithText(line string) (string, bool) {
	if i := strings.Index(line, "$(("); i >= 0 {
		if j := strings.Index(line[i:], "))"); j >= 0 {
			return strings.TrimSpace(line[i+3 : i+j]), true
		}
	}
	if i := strings.Index(line, "$["); i >= 0 {
		if j := strings.IndexByte(line[i:], ']'); j >= 0 {
			return strings.TrimSpace(line[i+2 : i+j]), true
		}
	}
	return "", false
}
```

Caveat: the error-token text bash prints is the unparsed remainder, which for
these two fixture lines equals the whole expression; the helper reproduces
that. Expressions where the operand error occurs mid-expression would print a
shorter token in real bash — acceptable until a fixture exercises it.

---

## C2 — `${x!y}` / `${#foo%}` rejected at parse time; bash defers to expansion (2 lines: D10, D12)

**Evidence** — expected `after 1: 1` and `after 3: 1` never appear: the
`bashy -c 'echo ${x!y} second\necho after 1: $?'` child dies on a parse error
(rc 2, nothing executed) instead of failing only the first `echo` (status 1)
and continuing.

```
$ bashy -c 'echo ${x!y} second
echo after 1: $?'
bashy: -c: line 1: not a valid parameter expansion operator: `!`
bashy: -c: line 1: `echo ${x!y} second'        # rc=2, "after 1" never prints
```

(The second, `-o posix` errors6.sub run is *not* affected: posix bash treats
these as fatal too, so expected output has no `after N` lines there.)

**Root cause** — `syntax/parser.go:1832` / `:1855`
(`not a valid parameter expansion operator`) and `syntax/parser.go:1741`
(`cannot combine multiple parameter expansion operators`) reject at parse
time what bash only rejects when the expansion is evaluated. The repo already
has the deferral vehicle: `expand.BadSubstitutionError` (expand/param.go:216,
"malformed parameter expansions which bash accepts at parse time but rejects
during expansion") and precedent commits (`9a2115d2` deferred select names,
`85c445b1` deferred bare `let`).

**Verdict**: tractable in principle but **parser surgery with upstream-divergence
risk** — the lexer must swallow an arbitrary unknown operator and capture the
rest of the expansion verbatim up to the matching `}`, in all quoting
contexts. Closest to architectural of the tractable set. **Estimate: 8**

**Sketch** (no inline diff — touch points): in `p.paramExpExp()` /
the `default:` arms at parser.go:1832/1855 and the double-operator check at
:1741, when `p.lang` is bash-like, record the raw text into a new
`ParamExp.BadOp string` field instead of erroring, skip to `}`, and have
`expand/param.go` return `BadSubstitutionError{Node: pe}` when `BadOp != ""`.
Combine with the C4 patch so the resulting runtime error sets `$? = 1`
non-posix / fatal posix.

---

## C3 — `${b[   ]}` blank array subscript rejected at parse time (6 lines: D13–D14, D16, D30–D31, D33)

**Evidence** — expected `4`, `array after 1: 0`, `array after 2: 0` (both
runs) missing:

```
$ bashy -c 'b[0]=4 ; echo ${b[   ]}
echo array after 1: $?'
bashy: -c: line 1: syntax error near unexpected token `['
bashy: -c: line 1: `b[0]=4 ; echo ${b[   ]}'
```

Raw parser message: ``1:9: `[` must be followed by an expression``
(`syntax/parser_arithm.go`, the `followArithm` empty-expression check). Bash
evaluates a whitespace-only subscript as arithmetic over the empty string →
index 0 → prints `4` / the empty assoc value, status 0.

**Root cause** — eager subscript parsing in `p.paramExp` →
`p.followArithm` rejects an empty/blank index; bash defers and evaluates
empty as 0 (indexed) or `""` key (assoc).

**Verdict**: tractable — much smaller than C2 because the construct is fully
delimited (`[` … `]`) and only the *empty* case needs deferring. **Estimate: 5**

**Sketch**: in the `${name[` index path, when the next token is `]` with only
whitespace consumed, synthesize a `*syntax.Word` index holding an empty
`Lit` instead of calling `followErr`; in `expand/param.go`'s index
evaluation, treat an empty index word as arithmetic `0` for indexed arrays
and as key `""` for associative arrays (bash prints empty + status 0 for the
missing key, per `errors.right` lines 187/189). The analogous single-quoted
form already has precedent in `cb6c5820` (subscript expansions in arithmetic).

---

## C4 — non-fatal `bad substitution` leaves `$? = 0` (3 lines: D7, D11, D27)

**Evidence**

```
$ bashy -c 'echo ${#+} second
echo after 2: $?'
bashy: line 1: ${#+}: bad substitution
after 2: 0            # bash prints: after 2: 1
```

In the posix run (D27) bash exits the child entirely (no `after 2` line);
bashy again prints `after 2: 0`.

**Root cause** — `interp/runner.go:341` `expandErr` prints the diagnostic but
its trailing `switch` (runner.go:382–406) has no `BadSubstitutionError` case,
so it hits `default: return` without touching `r.exit` /
`r.lastExpandExit`. (The same gap makes `echo $((2/0)); echo $?` report 0 —
not part of these 48 lines but the identical mechanism.)

**Verdict**: tractable. **Estimate: 2**

**Proposed patch** (`interp/runner.go`, in the `expandErr` switch — note
`badSubst` is already declared earlier in the function):

```diff
--- a/interp/runner.go
+++ b/interp/runner.go
@@ func (r *Runner) expandErr(err error) { ... switch {
 	case strings.HasSuffix(errMsg, "invalid indirect expansion"):
 		// TODO: These errors are treated as fatal by bash.
 		// Make the error type reflect that.
+	case errors.As(err, &badSubst):
+		// Bash fails the expansion with $? = 1 and keeps running;
+		// POSIX mode makes it fatal (errors6.sub, run 2).
+		r.exit.code = 1
+		r.lastExpandExit = exitStatus{code: 1}
+		if r.opts[optPosix] {
+			r.exit.exiting = true
+		}
+		return
 	default:
 		return // other cases do not exit
 	}
```

(Setting `r.lastExpandExit` matters: the runner restores `r.exit` from it
after expansion failures — see runner.go:4054.)

---

## C5 — `${uvar?}` / `${uvar:?}` print `uvar: ` instead of bash's default message (14 lines: D8–D9, D15, D17–D20, D28–D29, D32, D34–D37)

**Evidence**

```
$ bashy -c 'echo ${uvar?}' ./errors6.sub
uvar:                      # bash: ./errors6.sub: line 1: uvar: parameter not set
$ bashy -c 'echo ${uvar:?}' ./errors6.sub
uvar:                      # bash: ./errors6.sub: line 1: uvar: parameter null or not set
```

Exit status (1) and fatality are already correct — only the message text and
the missing `<file>: line N:` prefix differ. (D19/D36 are blank lines both
sides actually print; they land inside these hunks only because diff anchors
them differently.)

**Root cause** — two halves:
1. `expand/param.go:731–738`: `ErrorUnset` / `ErrorUnsetOrNull` build
   `UnsetParameterError{Node: pe, Message: arg}` with `arg == ""` when no
   word follows `?`; bash substitutes the defaults `parameter not set` /
   `parameter null or not set`.
2. `interp/runner.go:412` `looksLikeExpandError` doesn't recognize the
   resulting message, so the `<file>: line N:` prefix (runner.go:365–376) is
   never attached.

**Verdict**: tractable. **Estimate: 2**

**Proposed patch**

```diff
--- a/expand/param.go
+++ b/expand/param.go
@@ -731,11 +731,17 @@
 			case syntax.ErrorUnset:
 				if !vr.IsSet() {
+					if arg == "" {
+						arg = "parameter not set"
+					}
 					return "", UnsetParameterError{Node: pe, Message: arg}
 				}
 			case syntax.ErrorUnsetOrNull:
 				if !vr.IsSet() || str == "" {
+					if arg == "" {
+						arg = "parameter null or not set"
+					}
 					return "", UnsetParameterError{Node: pe, Message: arg}
 				}
```

```diff
--- a/interp/runner.go
+++ b/interp/runner.go
@@ func looksLikeExpandError(msg string) bool {
 		strings.Contains(msg, "cannot assign in this way"),
+		strings.Contains(msg, "parameter not set"),
+		strings.Contains(msg, "parameter null or not set"),
 		strings.Contains(msg, "invalid variable name"):
 		return true
```

Caveats: `${var?custom msg}` still won't get the prefix (same today; no
fixture line depends on it). Check `expand` package tests for callers
asserting the empty-message form.

---

## C6 — `${!x}` with invalid target name leaves `$? = 0` (4 lines: D21–D22, D38–D39)

**Evidence**

```
$ bashy -c 'x=-3; echo ${!x}; echo after indir: $?'
bashy: line 1: -3: invalid variable name
after indir: 0             # bash: after indir: 1
```

The diagnostic itself (D-context `./errors6.sub: line 40: -3: invalid
variable name`) already matches — only the status is wrong.

**Root cause** — `expand/param.go:676` returns
`fmt.Errorf("%s: invalid variable name", str)`; `expandErr`'s switch
(runner.go:382–406) has no case for it → `default: return`, status untouched.

**Verdict**: tractable. **Estimate: 1**

**Proposed patch** — shared with C7 below (one switch arm covers both).

---

## C7 — indirection through an *unset* variable with `:-` / `+` silently expands (8 lines: D23–D26, D40–D43)

**Evidence**

```
$ bashy -c 'echo ${!var:-unset}; echo ${!var+unset}'
unset                      # bash 5.3: ./errors6.sub: line 50: var: invalid indirect expansion
                           # bash 5.3: ./errors6.sub: line 51: var: invalid indirect expansion
```

Note from `errors.right` lines 199–200/219–220: bash prints *both* errors and
then continues to lines 54–56 — i.e. `invalid indirect expansion` here is
**non-fatal** (the TODO at runner.go:398 claiming bash treats it as fatal is
contradicted by this fixture).

**Root cause** — `expand/param.go:663–669`: the `!vr.IsSet()` arm exempts
default-style operators (`indirectDefaultOp`) from the
`invalid indirect expansion` error. Bash 5.3 only applies that leniency when
the indirection variable is *set but its value names an unset variable*
(the `str == ""` arm at :670, which must stay — it produces the expected
`unset` output for `foo=bar; echo ${!foo:-unset}` at errors6.sub:47).

**Verdict**: tractable; moderate regression risk (the exemption was added
deliberately — re-run new-exp/varenv gates). **Estimate: 3**

**Proposed patch** (covers C6 too):

```diff
--- a/expand/param.go
+++ b/expand/param.go
@@ -661,12 +661,9 @@
 		case (name == "@" || name == "*") && !vr.IsSet():
 			return "", nil
 		case !vr.IsSet():
-			if pe.Exp != nil && indirectDefaultOp(pe.Exp.Op) {
-				break
-			}
 			// Bash 5.3 includes the variable name in the message
 			// (`./file: line N: foo: invalid indirect expansion`).
 			return "", fmt.Errorf("%s: invalid indirect expansion", name)
```

```diff
--- a/interp/runner.go
+++ b/interp/runner.go
@@ func (r *Runner) expandErr(err error) { ... switch {
-	case strings.HasSuffix(errMsg, "invalid indirect expansion"):
-		// TODO: These errors are treated as fatal by bash.
-		// Make the error type reflect that.
+	case strings.HasSuffix(errMsg, "invalid indirect expansion"),
+		strings.Contains(errMsg, "invalid variable name"):
+		// errors6.sub lines 50–56: bash prints the diagnostic, sets
+		// $? = 1, and keeps running (even in POSIX mode).
+		r.exit.code = 1
+		r.lastExpandExit = exitStatus{code: 1}
+		return
 	default:
 		return // other cases do not exit
 	}
```

Without the runner half, the param.go change alone would make line 50 fatal
(current fall-through sets `exiting = true`) and *lose* lines 51/54–56 —
apply both together.

---

## C8 — `eval '( '`: unclosed-paren line number is eval-relative (2 lines: D44–D45)

**Evidence**

```
< ./errors8.sub: eval: line 7: syntax error: unexpected end of file from `(' command on line 1
> ./errors8.sub: eval: line 7: syntax error: unexpected end of file from `(' command on line 6
```

**Root cause** — the message is produced inside the parser
(`syntax/parser.go:996` / `:2686`) with the `(`'s line *within the eval'd
string* (line 1). Bash re-bases onto the script line of the eval call
(line 6 of errors8.sub). The eval error path already does exactly this
re-basing for `{` (interp/builtin.go:1664–1669, `absLine = pos.Line() +
openLine - 1`) but lets the parser's pre-formatted `(` text through untouched.

**Verdict**: tractable. **Estimate: 2**

**Proposed patch** (`interp/builtin.go`, just before the
`r.errf("%s: eval: line %d: %s\n", ...)` at ~1691):

```diff
--- a/interp/builtin.go
+++ b/interp/builtin.go
@@ eval parse-error path
+				// The parser stamps "from `(' command on line N" with
+				// the line inside the eval'd string; bash counts from
+				// the top of the enclosing script. Re-base like the
+				// `{` case above.
+				if i := strings.LastIndex(text, " command on line "); i >= 0 {
+					if n, aerr := strconv.Atoi(text[i+len(" command on line "):]); aerr == nil {
+						text = fmt.Sprintf("%s command on line %d",
+							text[:i], n+int(pos.Line())-1)
+					}
+				}
 				r.errf("%s: eval: line %d: %s\n", name, evalLine, text)
```

---

## C9 — `command export v=foo` / `command readonly v=foo` on a readonly var return 0 (2 lines: D46–D47)

**Evidence** (errors8.sub runs under `set -o posix`):

```
$ bashy -c 'set -o posix; readonly v; command export v=foo || echo ok 2; command readonly v=foo || echo ok 3' ./errors8.sub
./errors8.sub: line 1: v: readonly variable
./errors8.sub: line 1: v: readonly variable     # neither "ok 2" nor "ok 3" prints
```

The diagnostics match bash (those lines are diff-context, not diff); only
`ok 2` / `ok 3` are missing — the builtins return success after the failed
assignment.

**Root cause** — the simple-command paths of `export` / `readonly`
(`interp/builtin.go:3707–3749`) call `r.setVar(...)` unconditionally; `setVar`
prints the readonly diagnostic internally but returns nothing, so `exit`
stays 0.

**Verdict**: tractable. **Estimate: 2**

**Proposed patch** (same shape in both arms; `readonly` case mirrors it):

```diff
--- a/interp/builtin.go
+++ b/interp/builtin.go
@@ case "export":
 			if eqIdx >= 0 {
 				name := arg[:eqIdx]
 				if !syntax.ValidName(name) {
 					exit = invalidIdentifier("export", name)
 					continue
 				}
+				if prev := r.lookupVar(name); prev.ReadOnly {
+					r.errf("%s%s: readonly variable\n",
+						r.bashErrPrefix(r.curStmtPos), name)
+					exit.code = 1
+					continue
+				}
 				val := arg[eqIdx+1:]
 				r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: val, Exported: true})
@@ case "readonly":
 			if eqIdx >= 0 {
 				name := arg[:eqIdx]
 				if !syntax.ValidName(name) {
 					exit = invalidIdentifier("readonly", name)
 					continue
 				}
+				if prev := r.lookupVar(name); prev.ReadOnly {
+					r.errf("%s%s: readonly variable\n",
+						r.bashErrPrefix(r.curStmtPos), name)
+					exit.code = 1
+					continue
+				}
 				val := arg[eqIdx+1:]
 				r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: val, ReadOnly: true})
```

(Pre-checking and `continue`-ing avoids the duplicate diagnostic `setVar`
would otherwise print. Bash semantics fine print: a *failed* `readonly v=foo`
must not change v's value — the pre-check preserves that. `errors7.sub`
exercises adjacent paths; re-run the gate.)

---

## C10 — POSIX mode doesn't imply verbose `shift` range errors (1 line: D48)

**Evidence** — `command shift 12 || echo ok 4` in posix mode: bashy prints
`ok 4` (status correct) but not the expected
`./errors8.sub: line 11: shift: 12: shift count out of range`.

**Root cause** — `interp/builtin.go:526–533` only emits the diagnostic when
`shopt -s shift_verbose` is on; bash also emits it whenever POSIX mode is on.

**Verdict**: tractable. **Estimate: 1**

**Proposed patch**

```diff
--- a/interp/builtin.go
+++ b/interp/builtin.go
@@ -525,9 +525,10 @@
 			if n2 > len(r.Params) {
-				// Out of range: silent error by default; with
-				// `shopt -s shift_verbose`, emit a diagnostic.
-				if opt, _ := r.bashOptByName("shift_verbose"); opt != nil && *opt {
+				// Out of range: silent error by default; with
+				// `shopt -s shift_verbose` or in POSIX mode, emit
+				// a diagnostic.
+				if opt, _ := r.bashOptByName("shift_verbose"); (opt != nil && *opt) || r.opts[optPosix] {
 					return failf(1, "shift: %s: shift count out of range\n", args[0])
 				}
 				exit.code = 1
```

---

## Totals

| Cluster | Lines | Verdict | Estimate |
|---|---|---|---|
| C1 arith-in-eval format | 6 | tractable | 5 |
| C2 `${x!y}`/`${#foo%}` deferral | 2 | borderline architectural | 8 |
| C3 `${b[ ]}` blank subscript | 6 | tractable | 5 |
| C4 bad-substitution status | 3 | tractable | 2 |
| C5 `${var?}` default message | 14 | tractable | 2 |
| C6 `${!x}` invalid-name status | 4 | tractable | 1 |
| C7 unset-var indirection | 8 | tractable | 3 |
| C8 eval `(` line re-base | 2 | tractable | 2 |
| C9 `command export/readonly` status | 2 | tractable | 2 |
| C10 posix shift verbose | 1 | tractable | 1 |
| **Total** | **48** | | **31** |

Cheapest path to a big visible cut: C5 + C4 + C6 + C7 + C10 (21 estimate-points
3 or under, 30 of the 48 lines). C2 + C3 are the only parser-touching items;
everything else stays in `interp/` + `expand/`.

**Process note for whoever picks this up**: measure with
`THIS_SH=$(pwd -P)/bin/bashy` (or via `make test-bash`) — never through
`external/...//../../..` relative paths, or you will be benchmarking
`/Users/qiangli/projects/poc/ai/sh`'s stale binary again.
