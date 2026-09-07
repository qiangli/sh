---
id: be84e6c6dedf
kind: task
title: 'Bash++ agentic MVP: bare modifier for functions, methods and script actions'
seq: 38
status: done
priority: p2
created: 2026-09-07T08:56:01.260063Z
sprint: 134
---

Execution 2026-09-07: accountable owner codex-gpt5.6-sol; internal helper
`/root/agentic_engine`, parser phase, edits only sh. Source-shape prerequisite
measured by `/root/agentic_corpus`; manager baseline syntax/typedjson/interp
passed before edits. Manager owns final verification and integration.

Delivery 2026-09-07: bare forms, positioned AST, printer/Walk/typed-JSON,
buffered/one-byte parsing and compatibility escapes implemented. Manager's
whole-module short gate passes on final source; both live products pass the
13-case/546-execution action/mode matrix. Current205-shape --check and
--posix-gate pass against GNU Bash5.3.15 and the rebuilt product. Runtime
enforcement shipped in the same integration, not an inert syntax-only marker.

User decision, 2026-09-07: one bare `agentic` contract. An action accepts input,
processes it and produces output or an error; this includes methods, functions,
scripts, commands, utilities and tools. Numeric levels are not language syntax.
This replaces this story's earlier mandatory-rung proposal.

Implement the source surface in the existing Bash++ dialect:
- `agentic func f(...) ... { ... }`, including existing receiver method forms.
- `agentic function f() { ... }` for shell functions.
- `agentic { ... }` for a script action or a region containing command/tool calls.
  It groups in the current shell without introducing a subshell or variable scope.
  A function literal can contain this block; no new literal spelling is needed.

Keep one modifier position/boolean, not a rung enumeration. Extend the existing
function nodes/parser and the smallest block representation. Preserve positions,
printer, Walk and typed-JSON round trips; buffered and one-byte reads must agree.
No new dialect, directive, per-command flag or generic prefix-command grammar.

Depends on the selected start-site evidence from corpus story `642584d1c9e9`.
The selected definitions and block are now measured: definitions Class R and
the exact block prefix Class E. A Class-E commitment
requires a decision-table entry, completing-context evidence, near-miss fallback
and command/quote escapes. Do not steal ordinary uses of the word agentic.
Numeric declarations fail after commitment; valid classic command forms retain
their existing behavior. Test Bash++ enabled/disabled and POSIX combinations.

Runtime activation and call enforcement belong to `1a1dc881f966`; this story
does not pass off a parsed but inert marker as the finished feature. Until that
dependency lands, execution of an admitted marked form must report unsupported
rather than silently behaving as an unmarked form. Lowering belongs to Sprint
117 story `72cd8bec4ac6`. No changes to BASHY_AGENTIC semantics.
