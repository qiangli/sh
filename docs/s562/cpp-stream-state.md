# C++ result transport and user stream state

Sprint 562, Story-ID `f75f7d3b9ef4`.

The upstream `basic-ifstream-class` examples at L38 and L144 can insert an
empty or missing file's stream buffer into `std::cout`. This sets failbit:
user stdout still matches the native oracle, but the old worker's subsequent
cout insertion silently loses the result frame.

## Narrow implementation

Starting from combined lifecycle baseline `779c0127`, construct the existing
hex-encoded protocol frame in a string and emit it with `std::fwrite` and
`std::fflush(stdout)`. Both successful values and error frames use this path.
Before emission, call the cout buffer's `pubsync` directly: ordinary ostream
flush is suppressed by failbit and can throw under a user exception mask.
Direct buffer synchronization also preserves pending output when the user has
turned off C/C++ stdio synchronization. A null or throwing user stream buffer
does not suppress the independent frame. No flags, masks, formatting, or buffers
are replaced. A failed C transport write/flush exits nonzero (74), preserving
native-failure handling rather than reporting successful delivery.

The decoder, native process wait, output after the frame, exit callbacks and
compiled-worker cleanup are unchanged. Frames still share stdout, so existing
multiple/malformed-frame rejection remains intentional. This change does not
create an authenticated side channel or repair a user-closed stdout descriptor.
No example source, file fixture, corpus, module generator, or golden is changed.

## Focused verification

`bash docs/s562/verify-cpp-stream-state.sh NEW_RESULTS` runs only the affected
native/interface and interpreted/compiled tests plus lifecycle controls. It:

- Requires both empty/missing regressions to fail against baseline `779c0127`
  specifically with `C++ worker returned no result frame`.
- Verifies byte-identical retained L38/L144 fixtures against triage hashes,
  then runs both through native/interface and interpreted/compiled execution.
- Runs real native C++ probes and interface calls for empty/missing input,
  badbit, exception masks, error frames and native nonzero exits.
- Checks exact body/callback ordering with unsynchronized buffered cout and
  verifies that callbacks observe unchanged stream state and exception masks.
- Runs interpreted/compiled equivalents and the existing C/C++ exit, destructor,
  malformed-frame and compiled-worker cleanup controls.
- Parses Go JSON events, rejecting skips, failures, missing tests and missing
  package completion. Logs retain the baseline, tool versions and source diff.

The native exception probe catches the domain exception in its standalone
entry driver; a separate interface call verifies the emitted error frame.

For manager-owned Linux verification, stage a bundle, `COMMIT`, the remote
runner and their `SHA256SUMS`. Run the following with explicit absolute paths:

```sh
bash run-cpp-stream-state-remote.sh STAGE_DIR ZIG_BIN GO_BIN
```

The runner verifies checksums and the checkout identity, uses private caches,
refuses reused output directories, and bounds the whole run to 600 seconds
(with a 10-second termination grace). It authenticates the retained Linux Zig
binary by SHA-256. Each package test also has a timeout.
Staging alone is not a Linux acceptance result. The exact retained cases are included in this focused gate; wider corpus
acceptance remains with the manager. No full corpus closure is claimed here.

Local validation on Apple clang passed the two required baseline reds, all
synthetic native/interface checks, the interpreted/compiled stream checks and
existing lifecycle controls. The exact L38/L144 native/interface and
interpreted/compiled tests also passed, with no skipped tests. Linux evidence
must be produced by the manager's staged run; it is not implied by local success.

## Linux staging correction

The initial Linux gate failed before product acceptance. The empty case's
native compiler was killed after 30 seconds while constructing Zig's bundled
libc++ (`filesystem/directory_entry.cpp`); the missing case also failed during
compiler preparation. The retained log contains nullability warnings and a
driver warning, not the two required missing-frame errors. The earlier C-only
atexit success with the same SDK does not demonstrate that cold libc++ can be
built within a native test's 30-second context.

The corrected runner first prepares the private provider cache with a small
stream-state probe. Both native test flags (`-std=c++17`) and worker flags
(`-std=c++20 -O0`) are exercised, with exact commands, timings, exit statuses,
and complete diagnostics retained in `provider/`. Preparation has a single
90-second bound inside the unchanged 600-second overall bound. The subsequent
30-second contexts and 90-second/2-minute/4-minute test bounds are unchanged.
No corpus cache is imported. The triage generator-oracle flags
`-fno-sanitize=all -w` are deliberately not adopted: they differ from the
product's default provider configuration. No compiler warning is filtered.

This addresses cold provider construction as a staging prerequisite; it does
not convert the original failed Linux run into evidence for the emitter.
If preparation or either baseline red fails, the corrected gate stops and
the source remains unaccepted pending investigation. Baseline assertion
diagnostics point to the retained log rather than duplicating megabytes into
the parent console.

## Third retained fixture follow-up

The exact `fstream-typedefs` L49 source and golden from triage checkpoint 19
are now covered alongside L38/L144 in native/interface and
interpreted/compiled tests. A separate baseline overlay check requires L49
to fail specifically with the missing result frame; all three exact fixture
subtests must pass on the candidate. The emitter needs no additional change.
The follow-up is staged as a separate immutable revision, leaving the
manager's in-flight `f7936de2` gate untouched. All existing timeout limits,
provider preparation, decoder controls and lifecycle checks remain in force.
