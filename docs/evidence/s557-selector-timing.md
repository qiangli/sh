# Selector scaling measurement correction

Sprint: #557; Story: #168; Story-ID: 61b1156f9de1.

The obsolete macOS full-shell run reported 1.251s for four fields and 5.971s
for 1024 fields (ratio 4.77). A focused developer-host CPU profile of the
unchanged fixture recorded only 220ms parent CPU over 2.95s elapsed; the test
also launches native dependency helper compilation for its final fmt.Println.
That helper mirrors the wide struct, so the timed interval included a varying
SDK/build-cache workload unrelated to selector resolution.

The fixture now prints its checksum through interpreted builtin println and
checks its stderr output exactly. Both 40000-read loops, the 4/1024 field counts,
and the ratio threshold of 4 remain unchanged. No production evaluator code
changed. A focused positive run measured 62.0ms/58.8ms (ratio 0.95); the bounded
one-iteration wide benchmark measured 32.2ms/op.

A temporary Go overlay disabled only the selector cache hit in
bashPPResolveSelectionIn. The unchanged guard then failed at 71.8ms/537.2ms
(ratio 7.49), confirming the fixture still detects the intended regression.
The overlay is diagnostic only and is not part of the committed source.
Hosted full-suite acceptance remains separate from these bounded checks.

Three further bounded positive repetitions passed at ratios 0.99, 1.02, and
1.03 (0.839s total package time). The negative control exited with failure as
expected; no repeated full suite or broad benchmark was run locally.
