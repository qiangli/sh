---
id: 2daf9ef04ad4
kind: task
title: Build Go example corpus as Bash++ differential tests
seq: 1
status: assigned
priority: p1
created: 2026-08-07T04:54:28.411321Z
weave: 31
assignee: qiangli
sprint: 113
---

Create a durable Bash++ language-coverage corpus from the complete official Tour
of Go, beginning at https://go.dev/tour/welcome/1. Pin the current website
source at golang/website commit 9f4a41694f5dd210de4ab12c86c0331739266182
(or record and justify a newer reviewed pin), copy every executable example
with the Go Authors BSD license and exact page/source provenance, and record all
material Bash++ adaptations. The historical golang/tour repository may be used
only where the current website source delegates to it, pinned independently.

Every Tour example must have a manifest row and one terminal result:
PASS (executed under Go and Bash++ with matching normalized observations) or
NOT-APPLICABLE with the precise standing Bash++ exception/capability reason.
No PLANNED row, missing fixture, zero denominator, or static-only parse check
satisfies this sprint. Deterministic examples compare stdout, stderr, status,
and declared effects byte-for-byte. Time, randomness, network, image, and
concurrency examples use checked-in deterministic adapters or narrow declared
normalizers and must still execute. Preserve license notices and make the
runner hermetic after an explicit source-refresh operation.

Go by Example remains an optional supplemental corpus with its separate CC BY
3.0 attribution; it cannot substitute for any Tour row. Preserve Bash++ mode
off/on gates, focused parser/interpreter tests, race tests for concurrent
examples, and the complete upstream Go-language corpus gate in story
6f0c4d9a31be. Commit locally for conductor review; do not push.
