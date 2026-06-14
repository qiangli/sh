# Errors Fixture Blockers

> **2026-06-11 R2 note**: full 48-line clustering of the residual diff now
> lives in [ERRORS-ANALYSIS-R2.md](ERRORS-ANALYSIS-R2.md). That doc also
> explains why earlier counts (70/71) didn't move: the
> `external/bash-5.3/tests/../../../bin/bashy` measurement path resolves
> through the `external` symlink into a *different checkout's stale binary*.
> Measure with `THIS_SH=$(pwd -P)/bin/bashy` from the repo root instead.

## Cluster 2: unset /bin/sh behavior — LANDED

**Problem**: `unset /bin/sh` reports "not a valid identifier" but bash 5.3 stays silent.

**Root Cause**: In `interp/builtin.go`, the unset builtin validates that arguments are valid identifier names before attempting to unset them. However, bash 5.3 silently ignores arguments that can never be variable names (like paths containing `/`).

**Proposed Patch**:

```diff
--- a/interp/builtin.go
+++ b/interp/builtin.go
@@ -401,8 +401,12 @@ unsetOpts:
 					continue
 				}
 			}
-			if !syntax.ValidName(arg) {
-				return failf(2, "unset: `%s': not a valid identifier\n", arg)
+			// Bash silently ignores arguments that can never be valid
+			// variable names (like paths containing '/'), treating them
+			// as function name candidates.
+			if !syntax.ValidName(arg) && !strings.ContainsAny(arg, "/") {
+				r.errf("%sunset: `%s': not a valid identifier\n", r.bashErrPrefix(pos), arg)
+				exit.code = 2
+				continue
 			}
 		}
 		if nameref {
```

**Verification**:
- Before: `./errors.tests: line 97: unset: `/bin/sh': not a valid identifier`
- After: (no output, matching bash 5.3 behavior)

**Diff Impact**: Removes 1 line from diff

**2026-06-11 Update**: Applied locally in `interp/builtin.go` with
`interp/interp_test.go` coverage. Fresh gate numbers: errors 71, redir 0,
history 0, quotearray 48. Commit was blocked because the sandbox exposes
`.git` metadata read-only (`git add` cannot create `.git/index.lock`).

**2026-06-11 R2 Update**: The fix is now in the tree (a slightly different
shape — `interp/builtin.go:612` skips `/`-containing args under
`bashCompatErrors`), and the `unset: /bin/sh` line is gone from the diff.
The "errors 71" number above was measured through the stale-binary recipe;
the true residue with the sandbox binary is 48 lines (see
ERRORS-ANALYSIS-R2.md).
