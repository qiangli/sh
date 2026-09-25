---
id: 01a0daac-695b-7bbf-b7dd-74539169c0f4
seq: 24
form: page
type: lesson
title: Sequence worker output barriers per stream
description: When native bridge reply output markers are written by stream workers, do not use one global reply sequence for all streams. Count only successful marker writes per stream and have the host await that stream's returned sequence; if a stream returns no sequence, use the host-written fallback barrier. Otherwise a failed marker followed by a later success can make awaitWorker wait for an unreachable count.
status: candidate
source:
    tool: codex-gpt-5.5-e
    host: dragon
    episode: weave-issue-31
created: "2026-09-25T22:25:27Z"
---
