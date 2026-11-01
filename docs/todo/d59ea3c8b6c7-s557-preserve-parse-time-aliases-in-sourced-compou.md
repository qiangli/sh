---
id: d59ea3c8b6c7
kind: bug
title: S557 preserve parse-time aliases in sourced compound commands
seq: 175
status: assigned
priority: p1
created: 2026-11-01T22:42:38.673311Z
assignee: s557-test-readiness
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Modernish41356391458 atSh74697208 BLOCKED mktemp.mm310 done canonlyendloop, exit128. Loaderaliasforever='while :;' usedforever do in sourcedfunction; not='! ' also failscommandnotfound. Validate authoredminimalreproagainstBash5.3, inspectexisting aliasReparsePhysicalLine/withAliasReparse and eval aliasretry beforedesign. Correctsource aliasparse timing inclcompoundcommands, quoting, comments, here-docs, recursion/timing; no regexgloballyrewritingscripts. Reuseexistinglexer/parsermechanisms, preservealiasdefinitionexecutiontiming. Foreignsuiteonlyhosted; boundedauthoredtests+negativeguardslocally. Commitselectivenopush. Evidence/tmp/s557-modernish-41356391458.


## Delivery

The source builtin now resolves preexisting grammar-changing aliases at parser-identified command words before parsing their compound commands. An opt-in parser observer leaves ordinary parser callers unchanged. The source adapter reparses after each expansion, tracks alias ancestry to suppress recursive expansion per occurrence, without limiting the total number of legitimate alias uses. Plain command aliases retain the existing runtime path. It never executes a partial source tree to obtain more aliases.

Authored examples cover `forever='while :;'` inside a sourced function and nested loop, `not='! '`, pipelines, alias chains and self-reference, quoted/escaped names, comments, heredocs, arguments, disabled alias expansion, and aliases defined on the caller's source-invocation line. Fixed expected results are checked against GNU Bash when available; the local run used GNU Bash 5.3.15. Parser tests protect command-position boundaries, error interception, and default parser behavior. Positive cases preserve ordinary and negation aliases inside command substitutions through the existing runtime path. A negative guard preserves GNU's rejection of the tested `forever do ... done` alias form while locating a command substitution's closing delimiter. Parsing does not execute in-source alias definitions.

Validation: `GOWORK=off GOMAXPROCS=2 GOFLAGS=-p=2 go test ./syntax ./interp -run '^(TestCommandWordObserver.*|TestSourceGrammarAlias.*)$' -count=1 -timeout=60s` passed (syntax 0.219s; interp 0.532s). Twelve authored GNU differential cases passed. No foreign suite or full build ran locally. The full interpreter confirmation gate and Modernish corpus remain hosted validation requirements; this delivery does not claim they passed.

Scope limitation: source still parses a complete read unit before execution. Alias definitions, removals, or replacements within that source are not executed early or modeled as separate Bash read units. This patch fixes the observed loader-defined aliases in the subsequently sourced module; it does not implement general incremental source evaluation. Command substitutions retain their prior parser boundary. Recursion terminates using per-occurrence alias ancestry; there is no global valid-file rewrite limit. Reparsing after each replacement has quadratic worst-case cost in source length/alias occurrences; this is a remaining performance limitation of the scoped implementation.


### Review follow-up

Removed the original total 1024-rewrite cap because it rejected valid files with many independent aliases. Added a 1,030-occurrence regression, an indirect grammar-alias cycle, and positive command-substitution negation/ordinary-alias cases. The unchanged 60-second bounded selector passed (syntax 0.235s; interp 0.724s), including all 16 authored GNU Bash differential cases. A separate focused run of `TestSourceGrammarAliases/many-independent` passed in 0.20s (package 0.546s); this measures the authored case on one local host, not a scalability guarantee. Parent independently verified the initial commit's focused gate before this follow-up. Hosted corpus validation remains pending.
