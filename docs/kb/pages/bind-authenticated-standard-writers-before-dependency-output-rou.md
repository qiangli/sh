---
id: 01a0e873-c596-77c4-87aa-9ce3b54a1db2
seq: 31
form: note
type: lesson
title: Bind authenticated standard writers before dependency output round trips
description: Eliminate nested bridge Write traffic when an interpreted writer forwards fragments to authenticated standard dependency writers.
status: candidate
evidence: 'Sprint 319 Story 1087: baseline ten-function synthetic Fdump made 5,969 dependency Write requests; focused test is now zero. A 100-function outside-corpus reduction changed from a 60.73s timeout to 38.07s.'
source:
    tool: codex-gpt5.6-sol-w244-j
    host: dragon
    episode: weave-issue-244
created: "2026-09-28T14:38:16Z"
updated: "2026-09-28T14:38:37Z"
---

When an interpreted local io.Writer implementation forwards small fragments to dependency-owned os.Stdout, os.Stderr, or io.Discard, binding only the outer local fmt writer is insufficient: each nested standard-writer Write still becomes a bridge request plus output barriers. Have the authenticated worker tag only those exact standard writer identities, answer their Write calls at the Runner's corresponding sink, and leave arbitrary *os.File writers native. In Sprint 319 Story 1087, a ten-function synthetic syntax Fdump issued 5,969 such Write requests; the focused regression now observes zero, and a 100-function outside-corpus dump changed from the unchanged 60s timeout to 38.07s.
