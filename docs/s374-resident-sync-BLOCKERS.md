# BLOCKERS: ken/chan.go still misses 60 seconds

Baseline c3aa0e0c25fa40fcc2dd12f3be9768440547fb0e on novidesign.local:
60.000949666 s, context deadline exceeded; 636,829 native Lock and 636,829
native Unlock requests, two Mutex allocations, four equality requests and zero
channel requests.

Three serious prototype attempts:

1. First root run stopped at 0.919570542 s because the separate captured-value
   ownership check still required a worker session for resident values. Fixed
   that check and validated captured Mutex/WaitGroup identities with go run.
2. Corrected run: 60.000981958 s, context deadline exceeded. Zero native sync
   requests, zero native channel requests, four native equality requests.
3. Bounded CPU diagnostic: 60.000794875 s, same failure and request counts.
   Profile duration 60.32 s, 86.69 s samples: pthread_cond_signal 53.16 s
   (61.32%), pthread_cond_wait 22.44 s (25.89%), pthread_kill 1.98 s (2.28%),
   usleep 1.97 s (2.27%). Native socket traffic is no longer the bottleneck.
   Scheduler/condition-variable activity dominates; this does not establish
   whether the root is slowly progressing or stuck in its polling loop.

Stop root investigation under the three-attempt rule. The working Mutex and
WaitGroup prototype and measured loop speedup are committed; the 60-second root
acceptance remains unmet. Do not increase its deadline or alter the fixture.
RWMutex, Once and atomic value methods also remain future work.

Remote reproduction, with PATH=$HOME/sdk/go1.27.1/bin:$PATH, GOTOOLCHAIN=local:

    S374_REMOTE_KEN=1 go test -tags full -count=1 -timeout=80s -run '^TestS374OriginalKenChannel$' -v ./interp/
    S374_REMOTE_KEN=1 go test -tags full -count=1 -timeout=80s -run '^TestS374OriginalKenChannel$' -cpuprofile=/tmp/s374-sync-ken.cpu -o /tmp/s374-sync-interp.test -v ./interp/
    go tool pprof -top -nodecount=18 /tmp/s374-sync-interp.test /tmp/s374-sync-ken.cpu

Outer subprocess caps were 180 s for each test invocation and 30 s for pprof.
The files above remain on novidesign.local, under /tmp; checkout ~/s374/sync.

The required Bash comparison gate was run remotely (180 s outer cap):

    go test -tags full -count=1 -timeout=120s -run '^TestRunnerRunConfirm$' ./interp/

It failed in 6.197 s at /0049 (printf string zero padding), /0159 (strftime %q),
and /0856 (closed-stderr diagnostic), against GNU Bash 5.3.20. These exact three
failures are already baseline-confirmed in s374-resident-BLOCKERS.md. No Bash
comparator implementation or fixture was changed by this prototype.
