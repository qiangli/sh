---
id: b07d48131b64
kind: task
title: 'Sprint 118: repair selector assignment storage'
seq: 61
status: done
priority: p0
created: 2026-09-10T01:07:16.846175Z
sprint: 118
closed: 2026-09-10T01:34:34.466207Z
---

Candidate024 exact bounded replay on canonical sh e3615678 advanced examples/range-over-iterators/range-over-iterators.go past generic receiver binding but now fails interpreted exit 2: BASHPP-ESELECTOR-TYPE: assignment parent is not struct storage. Oracle and compiled pass. Diagnose and implement the smallest general selector-assignment storage correction, preserving value/addressability semantics and rejecting selectors whose parent is genuinely not assignable struct storage. Do not special-case the fixture, edit corpus/evidence, or overlap generic constraint logic. Add negative-first focused tests and run focused tests plus env GOMAXPROCS=2 GOFLAGS=-p=2 go test ./gosource ./lower. Required trailers Sprint #118 and this Story/Story-ID.
