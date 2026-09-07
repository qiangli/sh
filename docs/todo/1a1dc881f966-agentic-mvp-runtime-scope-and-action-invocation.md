---
id: 1a1dc881f966
kind: task
title: 'Agentic MVP: scope and invocation across functions, scripts and tools'
seq: 39
status: done
priority: p2
created: 2026-09-07T15:26:32Z
sprint: 134
---

Execution 2026-09-07: accountable owner codex-gpt5.6-sol; internal helper
`/root/agentic_engine` owns runtime after its serial parser work. Parser and
typed-JSON local packages pass; manager independently verifies the final gate.

Delivery 2026-09-07: manager final whole-module short gate passes (29.497s),
and independent TestBashPPAgentic race gate passes (1.681s). Coverage includes
callable/method/interface/returned-handle identity, ordinary helpers/closures,
trap/signal/mapfile callbacks, eval/source, deferred scope, task/pipeline copies,
return/error/panic/cancellation and runner reuse. HandlerContext.Agentic is live;
declare -f preserves markers and export -f refuses marker loss. Both product
entry/mode matrices pass. Public contract: docs/bashpp-agentic.md.

Implement the boolean execution contract after parser story be84e6c6dedf.
An action is input -> output | error, including methods, functions, closures,
scripts, commands, utilities and tools. There is no numeric level or inferred
effect lattice. The modifier permits LLM assistance, without requiring a call
or granting provider/tool permissions. Old explicit AI commands remain valid.

An explicit agentic block enters scope. A marked function/method requires
agentic caller scope before entering its body, and runs with assistance enabled.
Ordinary helpers remain callable but run with assistance off, unless their own
source enters an explicit block. No declaration is inferred from its location.
Preserve normal argument evaluation; rejection is not a transaction rollback.

Reuse bashPPInvoke/bashPPEnterFrame/frame.leave and the shell-function call
path. Preserve declaration metadata on method/function values and interfaces;
check the resolved callable dynamically instead of adding effect types or a
call-graph pass. Function literals can opt in with a block in their body.

Scope must restore on return, failure, panic and cancellation. Existing task,
pipeline and subshell copies isolate the boolean; defer retains its scheduling
scope. eval uses current scope. Source/script entry starts with assistance off
and uses the file's own block; source return restores the caller's scope without
changing shell variable/working-directory semantics. Cover runner reuse too.

Expose the scope bit through existing HandlerContext so commands/utilities/tool
adapters can consume it, with normal argv, streams, errors and dispatch intact.
No process-global state or environment propagation. Cooperating external script
tools carry their own source block. This is not a sandbox or a ban on existing
explicit calls to AI commands. BASHY_AGENTIC is not a source declaration.

Gate: focused tests of positive/negative calls, higher-order and method dispatch,
restoration, dynamic sources and concurrent isolation; race-test new concurrency
cases. Ordinary/Classic/POSIX behavior stays unchanged. Export the same minimal
runtime seam for the product bridge 872f9f15885f and Sprint 117 lowering.
