# Native child shell parent identity

A carrier represents a compound background shell, while its interpreter runs
inside the original Go process. A separately launched Bashy child previously
reported the Go process as `$PPID`. Signaling that value from the compound job
therefore interrupted the original shell instead of reaching the background
job, including for the INT/QUIT signals ignored by an asynchronous list.

Preserve the carrier's identity through a private, one-hop parent PID bridge
when launching this same shell payload or its installed native launcher. The
standalone child validates the physical parent before consuming the logical
parent value. It snapshots that value across Reset and shell copies. Embedded
runners ignore the bridge, and scripts cannot export a stale copy onward.

The bridge applies to children of carrier-backed compound jobs. Primary
external background jobs and explicit exec replacements retain their existing
parent semantics. Unrelated native programs receive no bridge: their kernel
`getppid` is unchanged. No signal-target heuristic or change to `$$` is involved.

Validation covers a native child reporting its parent directly and through
command substitution, nested compound jobs, primary and exec exclusions,
standalone versus embedded consumption, stale and malformed values, Reset,
subshell inheritance, executable identity, and environment filtering. A public
GNU Bash 5.3 reducer confirms that signaling the child's reported parent with
INT and QUIT leaves the compound job and original shell successful.
