---
id: 6241d0790c9b
kind: bug
title: Preserve shell argv0 through ENOEXEC fallback
seq: 160
status: done
priority: p0
created: 2026-10-03T00:59:30.284698Z
assignee: codex-gpt6-sol
sprint: 355
sprint_id: 3a83ff48-7f8b-5be4-b0e6-e146762b2573
sprint_title: Profile D residual blocker triage and targeted closure
closed: 2026-10-04T07:13:05.10366Z
closed_by: codex-gpt6-sol
---

The one-file Bashy D shell sh_05.ex assertions #334/#343 regressed after output-parent environment fix. A no-shebang executable launched from argv0=sh falls back through interp/handler.go but re-execs os.Executable() as argv0=bashy, selecting the AgentOS route; its output reducer renders /home/vsctester as literal $HOME. Preserve the parent shell invocation name while executing the same physical binary, retain descriptor/signal handoff, and prove POSIX mode and error-word tilde expansion for a no-shebang child. Exact sh_05 TP49/58 targeted replay must return PASS without reintroducing sh_12:717 env leakage.

## Sprint 355 acceptance evidence 2026-10-04

ENOEXEC argv0 repair is merged at eb39f9dc8; the focused sh_05 TP49/58 replay passed, and full6 has no regression in those identities or sh_12:717. Historical raw journals and any pending formal certification decisions are unchanged.
