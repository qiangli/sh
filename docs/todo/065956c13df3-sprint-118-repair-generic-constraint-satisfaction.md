---
id: 065956c13df3
kind: task
title: 'Sprint 118: repair generic constraint satisfaction'
seq: 60
status: done
priority: p0
created: 2026-09-10T00:32:52.940968Z
weave: 106
assignee: qiangli
sprint: 118
closed: 2026-09-10T01:06:48.207879Z
---

Candidate024 exact bounded replay on canonical sh e3615678 advanced examples/generics/generics.go past the receiver error but now fails interpreted exit 1: BASHPP-EGENERIC-CONSTRAINT: []string does not satisfy constraint for S in SlicesIndex. Oracle and compiled pass. Diagnose and implement the smallest general correction to generic constraint satisfaction/type-set handling. Preserve source/corpus/evidence and do not special-case SlicesIndex or []string. Add negative-first focused tests covering accepted ~[]E-style constraints and rejection outside the type set; run focused tests plus env GOMAXPROCS=2 GOFLAGS=-p=2 go test ./gosource ./lower. Required trailers Sprint #118 and this Story/Story-ID.
