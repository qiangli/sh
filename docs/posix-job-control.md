# POSIX Issue 7 job-control boundary

The `bg`, `fg`, and `jobs` builtins have an OS-backed path for a simple
external asynchronous job started under monitor mode on Unix:

- the child is created as a process-group leader and its group ID is retained
  in the job table;
- `jobs -p` and the PID field of `jobs -l` report that process-group ID;
- `bg` sends `SIGCONT` to the whole recorded process group;
- `fg` requires the shell to own a controlling terminal, gives that terminal
  to the job's process group, sends `SIGCONT`, waits for the live job, returns
  its status, and restores the shell's foreground process group.

`interp/jobcontrol_issue7_unix_test.go` proves these effects with re-executed
test processes and a real pseudo-terminal. The tests cannot pass with a fake
PID, an already-closed completion channel, a state-only transition, or a
missing terminal handoff.

A host that supplies a `ProcessGroupCarrierProcess` strengthens this boundary:
the carrier advertises a stable process group before the asynchronous job
starts, every external pipeline component joins it, and `jobs -p`, job-spec
signals, `bg`, and `fg` retain that one identity even when a short-lived child
exits while another component is starting. If the group is stopped, the
runner records the stopped job and asks the carrier to continue only its proxy
leader; the real children remain stopped until the shell or caller resumes the
job. Bashy's Unix CLI carrier implements this optional contract.

## Explicit residuals

- Without an opted-in `ProcessGroupCarrierProcess`, pipelines still combine
  in-process runners and separately started external processes; they do not
  create one kernel process group for the entire pipeline. Pipeline job
  control therefore remains partial for those embedders.
- Pure-builtin and compound asynchronous jobs remain goroutines. A carrier can
  give them a kernel-visible identity, but it does not move their in-process
  terminal I/O into another process group; `fg` fails closed when no proven
  process group exists.
- A Unix runner without an owned controlling terminal fails `fg` rather than
  claiming a foreground transition. Non-Unix builds likewise fail the real
  foreground handoff explicitly; their portable synthetic bookkeeping remains
  available but is not POSIX process-group evidence.
- Diagnostic localization through `LC_MESSAGES`/`NLSPATH` is not implemented.
