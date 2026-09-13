---
form: page
type: lesson
title: Gate string carrier numeric conversions by source type
description: When a GoSource scalar reaches interp as constant.String but its declared source type is numeric, convert through the resolved underlying numeric type and leave ordinary string or missing-type carriers unhandled; this fixes uint64 runtime conversions without permissive string parsing or Classic changes.
status: candidate
source:
    tool: codex-gpt-5.5-r
    host: dragon
    episode: weave-issue-200
created: "2026-09-13T22:04:26Z"
---
