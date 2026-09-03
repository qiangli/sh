// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

// This file records the Day-1 DEFERRAL of brace-form `if` (StartGoIf) and the
// reasoning behind it, backed by the parser probes in
// bashpp_braceif_probe_test.go.
//
// DECISION: Explicit Day-1 deferral. StartGoIf stays unrecognized, BashPPIf
// stays unconstructed, and `if` is not a Day-1 start site. The decision is
// permanent for the bounded-lookahead start-site recognizer; a future
// implementation requires a DIFFERENT mechanism, not a relaxed bound.
//
// THE PROBLEM — WHY A BOUNDED RECOGNIZER CANNOT DECIDE.
//
// Every other Day-1 site decides within maxLookahead (64) bytes of the command
// position. The `if` site cannot, because stock bash 5.3 ACCEPTS `{` as an
// ordinary word in the condition of an `if`:
//
//	if { true; }          then echo yes; fi   # brace group in condition
//	if test -f {a,b}.txt; then echo yes; fi   # brace expansion word
//	if err != nil {       \n then echo b; fi  # `{` is just an argument
//
// So the brace alone does not commit. What commits is the ABSENCE OF `then`
// after the MATCHING closing brace — and finding the matching brace requires
// tracking brace depth through an arbitrarily long condition, which is
// unbounded by construction.
//
// The probes in bashpp_braceif_probe_test.go demonstrate this concretely:
//
//   - Probe 1 shows legal bash scripts where `{` is a condition word.
//   - Probe 2 shows nested braces in conditions, requiring depth tracking.
//   - Probe 3 constructs an input where `{` and `then` are both past byte 64,
//     proving the bounded window is too small.
//   - Probe 4 shows why `for` CAN have braces: its commit point is at a
//     structural boundary (loop expression fully consumed), not mid-condition.
//   - Probe 5 confirms the recognizer correctly returns noMatch.
//   - Probe 6 confirms the BashPPIf node is ready for eventual use.
//
// WHY ALTERNATIVE A (BOUNDED MECHANISM) IS UNSOUND.
//
// Three bounded strategies were considered and rejected:
//
// 1. "Decide at the brace." If `{` appears after what looks like a Go
//    expression, commit to Go-form. UNSOUND: `if err != nil {` is a legal bash
//    `if` condition where `{` is an argument word. Claiming it breaks the
//    Bash++ superset contract (Rule 1: NEVER LOSE).
//
// 2. "Require the whole form on one line." Restrict Go-form `if` to shapes
//    where `{ ... }` fits within 64 bytes. UNSOUND: the same restriction
//    cannot be applied to the shell form, so a multiline shell `if` whose
//    condition ends with `{` and continues with `then` on the next line would
//    be misclassified.
//
// 3. "Use a heuristic (no `then` in the same chunk)." A chunk boundary can
//    split `{` and `then` arbitrarily. The conservative answer at a boundary
//    would silently fall back to shell, which is the same failure mode that
//    killed three prior fix attempts for an unrelated defect.
//
// GRAMMAR CONSEQUENCES OF THE DEFERRAL.
//
// 1. `if err != nil { echo a }` stays a shell `if` condition containing the
//    arguments `err`, `!=`, `nil`, `{`, `echo`, `a`, `}` — which fails at
//    `then` because `then` is absent. This is the same error bash 5.3 gives.
//
// 2. `if err != nil { \n then echo a; fi` continues to work as a legal bash
//    `if` with a condition that happens to contain `{`.
//
// 3. No shell escape is needed for `if`, because `if` is not claimed.
//    Scripts using `if` as a command word (impossible — it is a reserved word)
//    are not a concern.
//
// ESCAPE CONSEQUENCES: NONE.
//
// Because `StartGoIf` is not a Day-1 site, there is no Class E row, no
// divergence to license, and no escape to publish. The `command if ...` escape
// is not needed and must not be documented as if it were.
//
// WHAT A FUTURE IMPLEMENTATION REQUIRES.
//
// The deferral is from the start-site recognizer, not from Bash++ itself.
// A future implementation must use a DIFFERENT mechanism, such as:
//
//   - A two-pass approach: parse the shell `if`, then rewrite if the condition
//     ends with `{` and no `then` was found. This works because the parser
//     already has the complete IfClause at that point.
//
//   - A speculative parse: try parsing as a shell `if`; if `followRsrv` for
//     `then` fails after a condition ending in `{`, reparse the condition as
//     a Go `if`. This is backtracking, which the parser avoids, so it would
//     need careful scoping.
//
//   - A syntax-level directive: `if! err != nil { ... }` (a new keyword) that
//     is Class R by construction (bash rejects `if!`). This avoids the
//     ambiguity entirely but changes the syntax.
//
// Each approach has its own trade-offs, and the choice belongs to a later
// sprint. The AST node (BashPPIf), the start site enum (StartGoIf), and the
// interpreter stub (bashPPIf) are all in place and ready.
//
// SPRINT 98 STORY 127 — DECISION RECORD.
//
// Story:     #127
// Story-ID:  201488c16014
// Decision:  Explicit Day-1 deferral
// Evidence:  bashpp_braceif_probe_test.go (6 probes, all passing)
// Mechanism: StartGoIf stays excluded from RecognizeStartSite
// Contract:  No divergence row, no escape, no grammar change
// Ready:     BashPPIf node, StartGoIf enum, bashPPIf interpreter stub
