---
id: da4959013114
kind: bug
title: S557 preserve compound alias parsing for Modernish test-runner control flow
seq: 184
status: assigned
priority: p1
created: 2026-11-02T01:50:37.861714Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Root-authorized scoped repair. New candidate2296c35 Modernish41373845874 exits1 before test summary: pinned cde49fa lib/modernish/tst/run.sh151 fi can only end if. Read exact pinned source without executing foreign suites locally; isolate minimal authored GNU5.3 differential, repair parser/alias ownership only if reproduced. Preserve fixture/assertions. Bounded authored tests locally, hosted Modernish acceptance; no full local build or suite. Internal helper repair_review under manager ownership; parent reviews integration.


Implementation checkpoint (internal helper under manager ownership):

- Pinned public source cde49fa37f93e943b9cb9b9536ce83836c4864ae defines LOCAL/LOOP with opening braces and BEGIN/END/DO/DONE with closing braces. Source alias resolution recognized openings only, leaving closers as runtime commands and making the surrounding if fail parsing.
- Authored paired-brace reproduction matched the hosted fi diagnostic; paired split braces, loop clauses and conditional clauses also failed on baseline while GNU Bash passed. Negative-control focused run failed in 0.431s.
- Minimal change recognizes closing brace and do/done/then/else/elif/fi heads at the existing lexer-identified command-word boundary. No scanner, source timing, recursion rules, early side effects or foreign fixtures changed.
- Nine new authored GNU differential rows cover paired braces, split braces, loop/conditional clauses, chain, literal/quoted/escaped/heredoc preservation, function-body block and elif. GNU rejects an aliased case terminator in the tested pattern position; no esac handling was added and a parser rejection guard preserves this boundary.
- Final bounded verification: GOWORK=off GOMAXPROCS=2 GOFLAGS=-p=2 go test ./interp -run '^TestSourceGrammarAlias' -count=1 -timeout=45s PASS 0.777s, including existing recursion, 1030-independent-alias, command-substitution and read-before-execute guards. The installed GNU Bash 5.3.15 oracle checks exact outputs/errors in the authored table.
- No local foreign-suite run, full build or full suite. Hosted Modernish rerun remains required; this is a focused repair, not a claim the full corpus initializes or passes. Parent independently reviews and owns integration.
