---
id: 51d6870ac553
kind: task
title: S557 preserve declaration aliases in evaluated export listings
seq: 189
status: done
priority: p1
created: 2026-11-02T04:02:28.592976Z
assignee: s557-test-readiness
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
closed: 2026-11-02T04:36:13.935178Z
closed_by: s557-manager
---

Hosted Modernish41385787600 exactrootbb4b1d2/Bashy75627fe2/Shc890bf4a progressed past BUG_TRAPFNEXI andBUG_TRAPUNSRE after188; now init0/selftest128 before numberedtests. FD9trace BUG_UNSETUNXP/isset-x aliases export to _Msh_issetExHandleExport, evals command export-p output, callbackmarker absent and dies export-p notparseable/exportnotaliasable. Sourcehypothesis: eval only expandRawAliasSource on parseerror; exportdeclarations parseasDeclClause and bypassnormalcallaliasexpansion. Diagnose authoredGNU5.3 comparison first; bounded runtimefix preserving quoted/escaped export, command bypass, alias recursion and POSIX/export output semantics. No foreignfixture edits orlocalforeignsuite, no timeout/assertionrelaxation. Separate fromacceptedscoped188 signalrepair; fullModernish verdict stillopen. Root integrates andhostedgates; isolatedworktree required.

Authored GNU5.3 differential baseline reproduced four failures: export and
readonly aliases bypassed their handler; a chained declaration alias did not
run; a multiline POSIX export listing failed to reach its handler. Baseline
failed in 0.570 seconds, with quoted/escaped/command/builtin bypass guards
already passing. No foreign suite was executed locally.

Eval now expands caller-defined declaration-command aliases at lexer-identified
command positions before parsing their assignment operands as DeclClause.
It shares the existing source-alias parser loop and ancestry termination,
while leaving ordinary eval alias execution and parse-error retry behavior
unchanged. Quoted/escaped words, arguments, here-documents, command/builtin
bypass and disabled alias expansion retain their existing behavior. Alias
creation/removal is not executed early during a read. Foreign fixtures and
export serialization are unchanged.

The new differential cases cover handlers, multiline export listings, direct
and indirect recursion, chains, local declarations, lexical bypasses and read
unit timing. Existing source grammar aliases passed race validation (combined
with the initial fifteen new cases: 2.955 seconds). Separate parser-observer
boundary guards also passed. Full hosted Modernish remains the acceptance
check; its previous original exit128/no-summary result is retained.

Final authored differential set: seventeen cases passed with the race detector
in 1.633 seconds. Existing source parse guards passed race validation in
1.401 seconds; lexer command-word boundary tests passed in 0.306 seconds.
No product build or full local suite ran.
