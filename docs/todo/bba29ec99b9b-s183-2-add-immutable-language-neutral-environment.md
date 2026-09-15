---
id: bba29ec99b9b
kind: task
title: S183.2 — add immutable language-neutral environment plans
seq: 114
status: todo
priority: p0
created: 2026-09-15T00:07:55.225919Z
assignee: qiangli
sprint: 183
---

Implement the shared EnvironmentPlan in sh/polyglot: source-relative read-only discovery to an explicit project/VCS boundary; bashpp.yaml/json ambiguity; recognized Python project/lock/runtime metadata; deterministic redacted explanation and content/runtime/platform fingerprint, including ordered canonical PYTHONPATH entries, absolute executable identity and pyvenv.cfg. Selection is: explicit source name, otherwise sole matching overlay, nearest recognized project, inherited active environment, then PATH; conflicts fail. BASHPP_PYTHON overrides only the selected runtime executable. Launch with a fixed plan cwd, no implicit cwd sys.path entry, user-site disabled, PYTHONHOME cleared, and only normalized recorded resolution inputs. Discovery must not execute Python, import modules, invoke managers, contact a registry, or mutate files. Add focused environment tests including cwd/home shadowing, PYTHONPATH changes and fingerprint invalidation. Sprint: #183.
