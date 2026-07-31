#!/bin/sh
# fmtcheck — the formatting gate. REPORTS, NEVER REWRITES.
#
# THE ONE implementation; every caller delegates here so the scope can never
# drift between them:
#   - scripts/hooks/pre-push (automatic, once core.hooksPath is set)
#   - `make fmtcheck`
#
# THIS FORK TRACKS UPSTREAM mvdan/sh, and upstream's CI already gates formatting
# with `gofmt -s` (simplify — stricter than plain gofmt). So this is NOT a second
# gate: it runs the SAME check, just at push time instead of ten minutes later.
# A local hook that is more lenient than CI is worse than no hook, because it
# teaches you the tree is clean when CI will say otherwise. If upstream's check
# changes, change this to match rather than diverging.
#
# # Why this fails instead of fixing
#
# The obvious version of this gate runs `gofmt -w` and commits the result. Do
# not write that version. gofmt does not only move whitespace: on doc comments
# it applies godoc's legacy typographic substitution, turning a pair of ASCII
# single-quotes into a curly closing quote.
#
# That is a silent content change, and it is not hypothetical — it has already
# corrupted a comment in a sibling repo that quoted shell syntax, leaving a test
# harness documenting a form that does not exist. Nothing reported it: the diff
# looks like formatting, and formatting diffs are what reviewers skim.
#
# So the rule is: a human decides. When a file legitimately cannot take gofmt's
# output, the fix is to restructure it — move the literal into an indented
# doc-comment code block, where the characters survive verbatim — not to accept
# a rewrite of what the code says.
#
# `make tidy` still runs `go fmt` and that is fine: it is an APPLY step someone
# asked for. The distinction is that a GATE must not silently change content.
#
# # Scope
#
# Tracked .go files only, so build output and scratch files never fail the gate.
set -eu

root="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$root"

files="$(git ls-files '*.go')"
if [ -z "$files" ]; then
	echo "fmtcheck: no tracked Go files"
	exit 0
fi

# shellcheck disable=SC2086
unformatted="$(gofmt -s -l $files)"
if [ -z "$unformatted" ]; then
	echo "fmtcheck: PASS ($(echo "$files" | wc -l | tr -d ' ') files)"
	exit 0
fi

echo "fmtcheck: FAIL — these files are not gofmt-clean:" >&2
echo "$unformatted" | sed 's/^/  /' >&2
echo >&2
echo "diff:" >&2
# shellcheck disable=SC2086
gofmt -s -d $unformatted >&2
echo >&2
echo "Fix with: gofmt -s -w <file>" >&2
echo >&2
echo "READ THE DIFF FIRST. gofmt rewrites doc-comment text as well as" >&2
echo "whitespace — notably a pair of ASCII single-quotes becomes a curly" >&2
echo "closing quote. If a file quotes shell or Go syntax in a comment, move" >&2
echo "the literal into an indented code block rather than accepting the" >&2
echo "rewrite; see the header of this script." >&2
exit 1
