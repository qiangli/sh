---
id: 2056c892ba8c
kind: task
title: 'S117: keep immediate channel outcomes inside the launch failure boundary'
seq: 47
status: done
priority: p0
created: 2026-09-07T19:27:34.030822Z
sprint: 117
closed: 2026-09-07T19:59:31.316979Z
---

Independent adversarial review found ready Receive, ready receive-select, default-select and closed-send-select Arm before any blocking wait. An immediately failing child can then admit a later task side effect, violating existing launch-order contract. Reproducer TestChannelReviewImmediateFailureDoesNotArmLaterLaunch /tmp/s117-channel-review. Fix channel.go only plus targeted tests, race GOMAXPROCS 1/2/4 repeated3; preserve cancellation and cleanup.
