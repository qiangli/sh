---
type: gotcha
title: Deadline measurements need kill-after and exact survivor scans
description: When measuring Bash++ interpreted roots with coreutils timeout, plain timeout 70 can leave the local bashy process alive after TERM and keep the wrapper waiting. Use timeout --kill-after for bounded runs, and do not trust broad pgrep -f bashy.real because other lane command lines can contain the same path; scan exact argv for this workspace runner.
status: candidate
source:
    tool: codex-gpt-5.5-p
    host: dragon
    episode: weave-issue-146
created: "2026-09-12T16:27:19Z"
---
