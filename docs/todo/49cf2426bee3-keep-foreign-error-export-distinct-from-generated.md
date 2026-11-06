---
id: 49cf2426bee3
kind: bug
title: Keep foreign error export distinct from generated Go error type
seq: 171
status: assigned
priority: p0
created: 2026-11-05T06:48:12.106142Z
weave: 27
assignee: s562-lower-predeclared
sprint: 578
sprint_id: 70ba48be-af89-5ab1-8a8d-42a15fc1ddbe
sprint_title: 'S562 follow-up: remaining fence corpus work'
---

Bounded lower symbol/type hygiene repair, 8 points30minutes, isolated reviewed d079711c. Exact TypeScript handbook-v1-Basic-Types-L319 declares legal unaliased functions error(message:string):never, fail(), infiniteLoop(), then shell echo compiled. Interpreted source passes; compiled fails LOWER-ETYPE error(function) is not a type. Preserve all authoredsource and expectedoutput; no renaming upstream function or declaring skippedbuild-only. Diagnose generated foreignwrapper/predeclared error type collision. Use hygienic internal type/reference handling while retaining user-visible exported callable names, shadowing semantics and aliases. Add unchanged exactfixture and nativeTypeScript proof plus lower/interpreted modes; negative/control usertype/function shadowing and ordinary typederror signatures. Parent executes remote gates, no heavylocal native suites. No general frontend relaxedtypechecking, no corpus/count changes. Read prior lower hygiene infrastructure before edits. DCO Sprint/Story trailers. Manager codex-gpt6-sol exact95835ea8; supervisorregression_prep. Unrelated clockrollback rejections remainstrict and outside scope.

Carried from S562 (operator-approved early close 2026-11-06); deferral is not PASS. Run/branch/evidence references: weave run 27, d079711c.
