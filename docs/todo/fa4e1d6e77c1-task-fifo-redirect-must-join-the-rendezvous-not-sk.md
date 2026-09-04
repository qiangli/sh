---
id: fa4e1d6e77c1
kind: task
title: Task FIFO redirect must join the rendezvous, not skip it
seq: 16
status: todo
priority: p0
created: 2026-09-04T22:49:17.591315Z
sprint: 115
---

bashPPTaskProbeOpen opens with O_NONBLOCK so a task never blocks before arming. On a FIFO that flag does not defer the wait, it cancels it: O_RDONLY|O_NONBLOCK succeeds with no writer and the descriptor reads EOF. Clearing O_NONBLOCK afterwards cannot recover a rendezvous that never happened. TestBashPPGoArmsBeforeBlockingFIFORedirect failed deterministically at GOMAXPROCS=1 and passed elsewhere only because the writer usually won the race. Fix: the probe classifies, then the task arms and RE-OPENS blocking through openFifoWithContextFunc against the retained directory fd, with a dev/ino identity check closing the re-open window. Second defect found underneath: the blocking open is interrupted by the runtime's SIGURG preemption and the raw openat returned EINTR verbatim, so the corrected open failed exactly when it was waiting correctly; openReadFifoWithContext and openFifoWithContextFunc now retry on EINTR.
