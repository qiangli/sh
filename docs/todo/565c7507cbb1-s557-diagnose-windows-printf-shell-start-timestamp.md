---
id: 565c7507cbb1
kind: bug
title: S557 diagnose Windows printf shell-start timestamp mismatch
seq: 181
status: done
priority: p1
created: 2026-11-02T00:55:16.928258Z
assignee: s557-test-readiness
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
closed: 2026-11-02T01:26:18.464955Z
closed_by: s557-manager
---

Bash5341365164988 WindowsprintfnewFAIL one-second starttimestamp difference in -2format; Linux/macprintfPASS. Rawretained /tmp/s557-bash53-41365164988/bash53-windows-latest/_temp/umbrella-validation/bash53-diffs/printf.want and.got. Diagnose exactBash5.3fixturecontract and starttimecapture, authoredminimalchecks only; do notmodifyforeignfixture/normalizeawaytiming/hidefailure. Windowsfullmeasurement82/86includesarith,array(old176),glob-testknownfilesystemlimitation. Readsourceandreportminimalfix/proof, isolatedworktreecurrentSh; nofullforeignsuite/productbuildlocal.


## Diagnosis and bounded evidence

The unchanged GNU fixture samples an external `date` process after shell initialization and compares that observed time with printf's `-2` startup timestamp. It assumes both observations fall in the same second. The retained Windows output reports the external sample at 19:19:45 and startup at 19:19:44, which is consistent with crossing a second boundary before the external sample. An earlier retained macOS run also reports this warning, so the mechanism is not inherently Windows-specific. Historical scheduling cannot be reconstructed from these timestamps alone.

The interpreter captures `startTime` once during initialization, preserves it in subshells, and supplies it to the formatter's `StartTime` field. An authored regression fixes startup to Unix 1700000000.999999999 and verifies exact second 1700000000 through direct printf, a subshell, and printf variable assignment. `GOWORK=off GOMAXPROCS=2 GOFLAGS=-p=2 go test ./interp -run '^TestPrintfShellStartTimeStable$' -count=3 -timeout=30s` passed in 0.441s.

An independent authored GNU Bash 5.3 script captures `-2`, waits 1.1 seconds, samples external date, and captures `-2` again. It asserts that startup remains equal while the later external time increases. It passed with startup 1793580999 and external time 1793581000. This demonstrates that equality with a later date sample is not guaranteed by the reference shell. No foreign suite was executed locally.

No product-clock change is justified by this evidence. The foreign fixture, runner normalization, thresholds, and recorded failure are unchanged. The original Windows printf result remains FAIL; an unchanged hosted rerun is required for fresh acceptance evidence. This delivery records the limitation and protects the actual startup-time contract, not a claim that the original failed measurement passed.
