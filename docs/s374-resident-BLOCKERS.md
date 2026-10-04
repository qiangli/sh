# BLOCKERS: original ken/chan still exceeds 60 seconds

Three bounded runs on novidesign.local (Darwin arm64, Apple M4 Max, Go 1.27.1)
failed the unchanged 60-second interpreter bound:

1. Prototype: 60.000998958 s, context deadline exceeded.
2. CPU profile: 60.001070167 s, context deadline exceeded.
3. Native request trace: 60.000905041 s, context deadline exceeded.

The trace measured **zero native channel requests**, 752,350 native Lock calls
and 752,349 native Unlock calls on sync.Mutex, two mutex allocations and four
native equality requests. All seven channel type occurrences are certified
resident (baseline: four resident, three native inferred occurrences).

The 60.23-second CPU profile collected 72.04 seconds of samples: rawsyscalln
30.73%, pthread_cond_wait 25.93%, kevent 24.96%, pthread_cond_signal 10.63%,
usleep 3.37%. The remaining request traffic is native mutex synchronization.
This establishes removal of channel transport, but does not distinguish pure
mutex bridge cost from a synchronization/progress problem in the root's polling
loop. Further root investigation is outside this completed channel prototype;
per the three-attempt rule, stop here rather than change synchronization APIs,
rewrite the fixture, increase its bound or fall back to native program execution.

Remote profile: /tmp/s374-resident-ken.cpu. Reproduce the traced root with:

    export PATH=$HOME/sdk/go1.27.1/bin:$PATH GOTOOLCHAIN=local S374_REMOTE_KEN=1
    go test -tags full -count=1 -timeout=80s -run '^TestS374OriginalKenChannel$' -v ./interp/

Run only remotely with an outer time cap (the delivery used Python
subprocess.run(..., timeout=120, check=True)). The test reads the SDK fixture
without modifying it and logs request counts even on failure.

The required Bash comparison gate also has three host-comparator discrepancies
against GNU Bash 5.3.20 on this remote host: TestRunnerRunConfirm/0049 (zero
padding with printf %s), /0159 (strftime %q), and /0856 (closed-stderr diagnostic).
The full gate reports 1,985 passing subtests, 14 skips and these three failures;
its parent test also reports failure. These are Bash comparator outputs, not
channel tests. All three reproduce with baseline production files from
72f6f3f85a397403bc32479046e222a1c4e8ed67 (0.568 s). Exact commands are
recorded in the outer story RESULT.md.
