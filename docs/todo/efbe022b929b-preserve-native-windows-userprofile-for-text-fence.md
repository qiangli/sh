---
id: efbe022b929b
kind: bug
title: Preserve native Windows USERPROFILE for text-fence processors
seq: 145
status: done
priority: p1
labels:
    - windows
    - fences
created: 2026-09-23T16:51:32.275987Z
assignee: codex-s250
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
closed: 2026-09-23T17:31:31.159425Z
closed_by: codex-s250
---

Windows BashSharp Tour advanced/dockerfile and advanced/k8s run a managed Podman child from polyglot.Text. The Bashy shell exports USERPROFILE as /c/...; callText receives raw execEnv and passes this POSIX spelling to the native Windows child. Upstream Podman getDefaultMachineVolumes sees empty filepath.VolumeName and panics before connecting to the now-running managed WSL2 machine. After USERPROFILE conversion, Podman runs but warns that the POSIX TEMP path is not a native absolute path. Convert OS-owned USERPROFILE, TEMP and TMP to native spelling at the native processor child boundary, preserve shell-visible values and BASHYENV behavior, add focused controls, rerun exact two Tour cases and unchanged full 40. Tour Story #6 df72ce1c1d16 owns marker removal.
