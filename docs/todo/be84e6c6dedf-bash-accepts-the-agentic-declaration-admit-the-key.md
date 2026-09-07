---
id: be84e6c6dedf
kind: task
title: 'Bash++ accepts the agentic declaration: admit the keyword, parse it, activate it under --bashpp'
seq: 38
status: todo
priority: p2
created: 2026-09-07T08:56:01.260063Z
sprint: 134
---

ADMIT THE agentic DECLARATION INTO BASH++ so that agentic xxx parses and is represented. This is the ADMISSION half. The LOWERING half is sprint 117 story 72cd8bec4ac6, whose acceptance already covers it: every certified typed node and Bash# feature lowers deterministically to ordinary Go with interpreted and compiled agreement on effects.

WHAT IT IS. A declaration on the DEFINITION of a function or method, carried in the source text, marking what rung the action may run at. Rungs 0 to 2 are deterministic-or-measured, 3 and above generative. It is INTRINSIC: it travels with the unit through inlining, goroutine spawn, script inclusion and compilation into a command, which is the property the whole design turns on - the same action packaged five ways must get the same answer. Design of record: docs/agentic-tool-duality-design.md section 14 in the umbrella, sprint 131.

IT CARRIES A LEVEL, NOT A BOOLEAN, and this is the substantive constraint on the grammar. A boolean collapses five rungs into two and does it in the wrong place: it can express the TRUST boundary at rung 2-to-3 and cannot express the COST boundary at 3-to-4 at all, so a free local band-1 model and a metered cloud model would become the SAME declaration. Whatever the spelling, the declaration must be able to carry a level.

THE START SHAPE IS ALREADY MEASURED, so test 1 is settled and needs no further work. tools/startsites/classify.sh was run with BOTH required oracles - bash 5.3.15 and bashy 5.3.0-dev bc9e466 - against a scratch corpus via the SHAPES override. Result: 8 shapes, 8 CLASS R, 0 class E, 0 engine disagreement, both oracles agreeing on every row. Shapes measured, at the COMMIT POINT rather than as complete forms: agentic function f() open-brace; agentic f() open-brace; agentic func f() open-brace; agentic 3 func f() open-brace; agentic(3) func f() open-brace; at-agentic func f() open-brace; agentic:3 func f() open-brace. CONSEQUENCE: every candidate spelling is PURELY ADDITIVE - no existing script can contain it - so Bash++ may claim the shape with NO TABLE ROW, NO NEAR-MISS FALLBACK and NO command-or-quote ESCAPE. The level-carrying parenthesised form agentic(3) is Class R too, which is another instance of the law this corpus already exposed: the parenthesised call is Bash++'s free disambiguator, since bash's only word-open-paren production is name-open-paren-close-paren. Committing these rows to the corpus and running --posix-gate is tracked separately as bashpp-tests story 642584d1c9e9.

THE OTHER THREE ADMISSIBILITY TESTS. Test 4, does not re-spell a construct Go already has, PASSES: Go has no effect system. Test 3, inert under --posix and invisible with the flag off, is satisfied by construction the same way the rest of the Bash++ surface is. Test 2, it lowers to ordinary Go, is the one that nearly refuses this and it belongs to sprint 117: Go has no rung, and section 14.9 argues the test is satisfiable in exactly ONE shape - the STATIC half ERASED, since a rung's job is to REFUSE illegal compositions such as a rung-3 callee inside a gate's contract check and a refusal need not survive lowering, plus a RUNTIME MARKER that is an ordinary value and lowers as a plain struct field or wrapper. A rung needing runtime enforcement in the lowering does NOT lower and must be REFUSED. That split is a DESIGN SKETCH, not a verified lowering.

SEMANTICS THIS SPRINT SHOULD SETTLE ALONGSIDE THE PARSE. ABSENCE IS REFUSAL, NOT ZERO: an undeclared unit is refused a rung above 0 rather than assumed deterministic, which is the refusal pkg/skills already makes when a contract-less skill is denied a capability key. And determinism is NOT preserved under composition, so a unit is at least as generative as its most generative callee and the effective rung is the max over the unit and its callees - an ordinary effect lattice. Whether the lattice computation lands here with the declaration or in 117 with the lowering is a decomposition call for the Bash++ lane, not for sprint 131.

SCOPE AND OWNERSHIP. Per section 14.8 the SYNTAX, the Bash# ergonomics interaction and the activation semantics are BASH++ DESIGN DECISIONS. Sprint 131 states what it needs and supplies the measurement; it does not choose among the seven spellings, and a Class R result says a spelling is FREE to claim, never that it SHOULD be claimed. If this lane re-homes, defers, refuses or respells it, that is the correct outcome.

NOT IN SCOPE. The command-level control needs nothing from Bash++ and is settled: BASHY_AGENTIC is to bash scripts what this keyword is to shell and Go functions, carried by the process environment and filling the AMBIENT slot, while the keyword is carried by the source text and fills the DECLARED slot. A per-command --agentic FLAG is INADMISSIBLE, because it would mutate every command's option surface, which is exactly what the POSIX suite measures, and layer 1 sits below Bash++ where a higher layer may not weaken a lower one.
