# Bashy Agentic Extensions — Design Catalogue

Date: 2026-05-26

Companion to `docs/bash-gap-analysis.md`. This file expands Step 5 of the
gap audit: features that would make bashy distinctly useful when an AI
agent is driving the shell — beyond plain bash compatibility.

Each item lists: why an agent cares, proposed surface (flag/env/builtin),
implementation sketch (which files), and risk/cost.

Bashy already has the right seams for most of this. `interp/api.go` exposes
`ExecHandler`, `OpenHandler`, `ReadDirHandler`, `StatHandler`, `CallHandler`
middleware. Almost every extension below slots into an existing handler or
adds one alongside.

---

## 1. Deterministic mode

**Why an agent cares.** Reproducible runs. If `$RANDOM`, `$SECONDS`, `$$`,
and `$EPOCHSECONDS` are stable across executions, an agent can compare
outputs from two prompts without spurious diffs and can write idempotent
smoke tests. It also defangs scripts that key on `mktemp` filenames.

**Surface.**
- `set -o deterministic` (new `set` option), and env trigger `BASHY_DETERMINISTIC=1`.
- Optional seed: `BASHY_DETERMINISTIC=42` to seed `$RANDOM` and the `mktemp`
  suffix generator deterministically.

**Implementation sketch.**
- New bool field `Runner.deterministic` + `RunnerOption WithDeterministic`.
- `interp/vars.go:lookupVar`:
  - `RANDOM` → seeded sequence (use `math/rand/v2.PCG`).
  - `SRANDOM` → reject (it's cryptographic by definition) or return zero.
  - `SECONDS`, `EPOCHSECONDS`, `EPOCHREALTIME` → fixed values from a
    `r.deterministicEpoch` field captured at runner construction.
  - `$$`, `BASHPID` → return `r.deterministicPID` (e.g. 1).
  - `$!` → counter-based, not the real OS pid.
- `interp/builtin.go` for `printf %(fmt)T` with `-1` (now) → use the same
  fixed epoch.
- `moreinterp/coreutils` `mktemp` → seeded suffixes.

**Risk/cost.** S–M. Low risk — the toggle is opt-in. Watch out for tests
that rely on the dynamic-`PPID` semantics.

---

## 2. Structured-output (`--json`) mode for in-process builtins

**Why an agent cares.** Parsing `jobs`/`declare -p`/`trap -p`/`type` output
with regex is a known source of agent errors. A schema'd JSON shape is
orders of magnitude more reliable.

**Surface.** A `--json` flag accepted by these builtins when implemented:
`jobs`, `declare -p`, `declare -F`, `trap -p`, `set` (with no args),
`set -o`, `shopt -p`, `type`, `times`, `kill -l`, `printf -v` (writes a JSON
value into the variable), `caller`.

Also: a global mode `BASHY_OUTPUT_JSON=1` that flips the default for the
above (scripts not opting in still print bash-shaped text).

**Implementation sketch.**
- In `interp/builtin.go`, add a small `jsonOut(any)` helper that uses
  `encoding/json`.
- Each builtin's flag parser learns `--json`. Where the builtin already
  iterates over an internal table (e.g. `bashOptsTable`), it dumps the
  table directly.
- Document each builtin's JSON shape in `docs/json-output.md`.

**Risk/cost.** S per builtin. Worth doing all in one batch so the schemas
look consistent.

---

## 3. `runner-state` introspection builtin

**Why an agent cares.** "Why isn't `$FOO` what I expected?" debugging in an
LLM is brutal because the agent can't actually `gdb` the runner. A single
`runner-state` builtin that dumps the live runner — vars, traps, hashed
commands, open fds, options, call stack — is a force multiplier.

**Surface.** New builtin `runner-state` (or `bashy-state`). Subcommands:
- `runner-state vars` — every variable + flags, JSON.
- `runner-state traps` — trap callbacks per signal.
- `runner-state fds` — open fdTable entries + stdio.
- `runner-state opts` — set / shopt state.
- `runner-state callstack` — `FUNCNAME`/`BASH_SOURCE`/`BASH_LINENO` zipped.
- `runner-state all` — everything.

Add `--json` (default) and `--text` fallbacks.

**Implementation sketch.**
- New file `interp/state_dump.go` with serializers reading from `Runner`.
- Builtin dispatcher case in `interp/builtin.go` near the end of the
  switch (before the default arm), gated on `IsBuiltin("runner-state")`.
- Add `"runner-state"` to `IsBuiltin`.

**Risk/cost.** S. Pure introspection; can't break anything except by
exposing things the runner shouldn't. Vet what gets emitted (no secrets
from env automatically).

---

## 4. Per-runner resource limits

**Why an agent cares.** A misbehaving script (infinite loop, fork bomb,
massive stdout) shouldn't be able to take down the agent host. The shell
should self-cap before the OS has to. Also serves as a graceful timeout
for "run this snippet, give me up to N seconds and M bytes."

**Surface.**
- `RunnerOption` set:
  - `WithMaxWallTime(d time.Duration)`
  - `WithMaxCPUTime(d time.Duration)` (best effort; use rusage on Unix)
  - `WithMaxOutputBytes(n int64)` per stream
  - `WithMaxChildProcs(n int)`
  - `WithMaxOpenFiles(n int)`
- New builtin `limits` listing current state.
- New env triggers `BASHY_MAX_WALL=30s`, `BASHY_MAX_OUTPUT=1MB`, etc.

**Implementation sketch.**
- Add a `Runner.limits` struct.
- In `Runner.Run`, kick a `context.WithTimeout` from `WithMaxWallTime`.
- In `ExecHandler` middleware, count children and reject past the cap.
- Wrap `stdout`/`stderr` with byte counters that error out at the cap
  (return a sentinel matching `errBuiltinExitStatus` with code 137).
- For `MaxCPUTime`, sample `syscall.Getrusage` periodically (cheap; bash
  itself has no equivalent).

**Risk/cost.** M. The wrap-stdio piece needs care to avoid changing
non-limit behaviour. Worth a feature flag.

---

## 5. Sandbox / path-allowlist mode

**Why an agent cares.** When a user says "run this script in my repo,"
the agent should be able to lock the shell to that directory tree. Bash's
`set -r` (restricted shell) is too crude (no cd, no /, no env tweaks);
modern containers are too heavy. A middle ground is path-level allowlisting.

**Surface.**
- `RunnerOption WithSandboxRoots([]string, []string)` (read-roots, write-roots).
- `BASHY_SANDBOX_READ=/repo,/tmp` and `BASHY_SANDBOX_WRITE=/tmp` env triggers.
- Reads outside the read-roots → ENOENT. Writes outside the write-roots →
  EACCES. Execs outside `PATH` allowlist → "command not found".
- New builtin `sandbox-status` to inspect.

**Implementation sketch.**
- Wrap `OpenHandler` / `StatHandler` / `ReadDirHandler` (they already exist).
  Each middleware checks the absolute path against the allowlists and
  returns the right errno.
- Wrap `ExecHandler` to deny external execs whose absolute path falls
  outside the read-allowlist (so the agent can't bypass via `/tmp/payload`).
- Builtins that bypass these handlers (currently very few) must be audited.

**Risk/cost.** M. Existing `_test.go` machinery has good coverage of
OpenHandler/StatHandler, so the middleware pattern is well-trodden.

---

## 6. Audit / provenance hook

**Why an agent cares.** "What did the LLM-driven shell actually do?" is a
recurring question. An audit hook fired pre-exec gives a verifiable trail
for embedders (CI, agents, dev tools) and is cheap to implement.

**Surface.**
- `RunnerOption WithAuditHandler(func(AuditEvent))`.
- `AuditEvent` struct: `Kind` (exec/builtin/trap/source/redir), `Args`,
  `Pos` (file/line/col), `When` (time), `CallStackHash`, `EnvDigest`.
- Optional env `BASHY_AUDIT_LOG=/path/to.jsonl` writes one JSONL line per
  event (no embedder code needed).

**Implementation sketch.**
- Single call site in `interp/runner.go` right before `r.cmd` dispatches a
  simple command, and inside `builtin.go` before the switch.
- The audit log writer should be lock-free or have its own goroutine to
  avoid blocking the runner.

**Risk/cost.** S. Lots of design ROI for very little code.

---

## 7. Dry-run / `--explain` mode

**Why an agent cares.** Show me what *would* happen before I let the shell
do it. Useful for: code review (LLM proposes a script, user reviews the
explanation), incremental approval flows, and writing tests against
"what should the shell do?" without side effects.

**Surface.**
- Bashy CLI flag `--dry-run`: parse the script and print one line per
  command that would execute (expanded args, after substitutions), without
  running anything.
- Alternative builtin `command --explain foo`: report how `foo` would be
  resolved (alias / function / builtin / hashed / PATH search hit).

**Implementation sketch.**
- `--dry-run` flips a flag in the Runner. Every leaf-command entry point
  prints `[+ would-run] cmd args...` and skips execution. Builtins and
  side-effecting expansions (`$(cmd)`) are special: `$(cmd)` still has to
  evaluate or substitution can't happen — flag this in the dry-run output.
- `command --explain` reuses the existing `typeMatches` helper in
  `builtin.go` and emits the resolution chain.

**Risk/cost.** M. Honest dry-run is hard because of `$(...)` and `${var:=default}`
side effects; document the semantics explicitly. `--explain` is S.

---

## 8. Capability declarations

**Why an agent cares.** Static analysis of "this script needs network and
fs-write" before running it. A capability line in the script preamble lets
the embedder decide whether to allow that combination. Pairs well with
the sandbox.

**Surface.**
- A magic comment in the first 4 lines: `# bashy: requires net,fs-write,exec`.
- A builtin `require fs-write` callable from script body (declarative,
  defensive against truncated preambles).
- Embedders set the set of granted caps via `RunnerOption WithCapabilities(set)`.
- Mismatch → fatal error before running anything.

**Implementation sketch.**
- Lex the preamble in `cmd/bashy/main.go` before parsing the body proper.
- Implement the cap set in `interp/api.go` as `Runner.grantedCaps`,
  `Runner.requestedCaps`.
- `require` builtin in `builtin.go` adds to `requestedCaps` and errors
  immediately if granted set doesn't cover.

**Risk/cost.** S–M. The capability vocabulary needs design: minimum useful
set is `net`, `fs-read`, `fs-write`, `exec`, `env-write`, `proc-create`.

---

## 9. Position-aware structured errors

**Why an agent cares.** Bash's `script.sh: line 42: foo: command not found`
is human-friendly but agent-unfriendly. Structured `(file, line, col,
function, errno_or_kind)` tuples are way easier to autocorrect from.

**Surface.**
- `RunnerOption WithStructuredErrors(func(ErrorEvent))`.
- `ErrorEvent` carries: `Kind` (parse/exec/builtin/expand/redir/trap),
  `Severity`, `Message`, `Pos`, `Function` (current FUNCNAME), `Command`,
  `Stderr` (last 64 KB).
- If unset, bashy keeps emitting human strings via stderr (no behavioural
  change).

**Implementation sketch.**
- Single helper `r.report(ErrorEvent)` called from `failf` and the parser.
- Existing `errf` continues to handle the stderr path. The handler is
  additive.

**Risk/cost.** S. Mostly mechanical.

---

## 10. Replay log / record-and-replay

**Why an agent cares.** Debugging a "the LLM did something weird" report
without re-running the whole flow. The shell records its inputs (commands,
env, stdin) and outputs (stdout, stderr, exit) to a JSONL file; a separate
`bashy --replay file.jsonl` re-evaluates and re-checks.

**Surface.**
- `BASHY_RECORD=/path/to.jsonl` env or `--record file` CLI flag.
- `bashy --replay file [--strict|--lax]` runs the script against the
  recorded inputs and diffs outputs.

**Implementation sketch.**
- A `tee` layer on stdin / stdout / stderr.
- A pre-exec audit hook (see #6) writes one JSONL line per command.
- Replay mode reads the JSONL, feeds stdin lines as they were, captures
  outputs, diffs against the recorded outputs.

**Risk/cost.** M. The strict-vs-lax knob is delicate (timestamps, PID-shaped
outputs). Best built on top of #1 (Deterministic mode) so the replay
diffs are tight.

---

## 11. Inline doc / `bashy explain <name>` helper

**Why an agent cares.** When the agent is unsure whether `mapfile -d` or
`read -d` is the right answer, an inline lookup is faster than a web fetch
and authoritative against this shell's actual feature set.

**Surface.**
- `bashy explain mapfile` → full doc + every flag bashy actually honours
  (with "(not implemented in bashy)" annotations).
- `help <name>` already exists but is bare; this is a richer
  bashy-specific variant.

**Implementation sketch.**
- `//go:embed help/*.md` Markdown files keyed by builtin name.
- Generated from the existing TODO + per-flag truth table.

**Risk/cost.** S; the value is in the *content*, not the plumbing.

---

## 12. Cooperative cancellation

**Why an agent cares.** "Stop, I changed my mind" arriving 5 seconds into
a 30-second command should reliably stop the shell without leaving zombie
children. Today the embedder can cancel the `context.Context`, but
goroutine-spawned subshells may not check.

**Surface.**
- Document that `Runner.Run(ctx, ...)`'s ctx cancellation cleanly
  terminates everything, including bg jobs spawned during the run.
- Add `RunnerOption WithCancelHook(func())` for "I'm about to die, run
  this first" cleanup.

**Implementation sketch.**
- Audit every loop in `interp/runner.go` for `ctx.Done()` checks. Add
  where missing.
- bg goroutines already use ctx; verify the kill propagation.
- `CancelHook` invoked from `Run` defer.

**Risk/cost.** M. Mostly auditing.

---

## 13. Embedder-supplied builtins via registry

**Why an agent cares.** "I have a tool that fetches data from $MCP_SERVER;
expose it as a shell builtin so scripts can call it directly." Right now
the embedder has to wrap it as an external command (and accept the
exec.Cmd overhead) or fork bashy.

**Surface.**
- `RunnerOption WithExtraBuiltins(map[string]BuiltinFunc)`.
- `BuiltinFunc` signature: `func(ctx, name, args, stdin, stdout, stderr) (uint8, error)`.
- They live in a per-Runner map; `IsBuiltin` returns true for them; the
  dispatcher's default arm tries this map before falling to `not supported`.

**Implementation sketch.**
- New `Runner.extraBuiltins map[string]BuiltinFunc`.
- Dispatcher in `builtin.go` checks this map before the `unsupportedHints`
  fallthrough.

**Risk/cost.** S. The API surface is small and additive.

---

## 14. Telemetry / metrics

**Why an agent cares.** "How long did the script spend in `find`?" Without
profiling, an agent can't suggest optimisations.

**Surface.**
- `RunnerOption WithMetricsHandler(func(Metric))`.
- One metric per builtin call and per exec: name, duration, stdin/stdout
  byte counts, exit status.

**Implementation sketch.**
- Wrap the builtin dispatcher and `ExecHandler` with a timer.
- Default no-op; only the embedder sees the data.

**Risk/cost.** S.

---

## 15. Per-script policy file

**Why an agent cares.** "When running scripts that look like CI bootstrap,
enforce these defaults: errexit, pipefail, nounset, no `eval`, log every
command." Encoded once, applied to every Runner instantiated against the
matching shebang or filename pattern.

**Surface.**
- A `~/.bashy/policy.toml` (or per-repo `.bashy.toml`) read at Runner
  init.
- Sections: `[[match]] glob = "*.sh"`, `[options] errexit = true`,
  `[deny] builtins = ["eval","trap"]`, `[caps] grant = ["fs-read","exec"]`.

**Implementation sketch.**
- New file `interp/policy.go` with TOML parsing (already in `go.sum`? if
  not, vendor `pelletier/go-toml/v2`).
- Applied as a layer of `RunnerOption`s before user-supplied ones.

**Risk/cost.** M. Policy semantics need careful design (precedence,
overrides, "policy.toml found but unreadable" failure mode).

---

## Quick comparison table

| Ext | Effort | Risk | Compat impact | First user value |
|-----|--------|------|---------------|------------------|
| 1. Deterministic mode | S–M | low | additive | reproducible runs |
| 2. JSON output | S each | none | additive flag | parseable output |
| 3. runner-state | S | none | new builtin | self-introspection |
| 4. Resource limits | M | low | additive | safety net |
| 5. Sandbox | M | low (existing handlers) | additive | locked-down runs |
| 6. Audit hook | S | none | additive | provenance |
| 7. Dry-run / explain | M | medium | additive flag | "what would happen" |
| 8. Capability decls | S–M | low | requires opt-in | static review |
| 9. Structured errors | S | none | additive | autocorrect signal |
| 10. Record / replay | M | medium | none default | repro-from-log |
| 11. Inline docs | S | none | new builtin | self-documenting |
| 12. Cancellation audit | M | low | none default | clean cancel |
| 13. Embedder builtins | S | low | additive | tool integration |
| 14. Metrics handler | S | none | additive | profiling |
| 15. Policy file | M | low | requires opt-in | per-repo discipline |

---

## Recommended first agentic batch

1. **#6 Audit hook** — paves the way for #10 (replay) and #4 (limits).
2. **#1 Deterministic mode** — small, immediately useful, no compat risk.
3. **#3 runner-state** + **#2 JSON output** on a starter set
   (jobs/declare-p/trap-p/shopt-p/set-o/type) — exposes everything bashy
   already knows in machine-readable form.
4. **#9 Structured errors** — riding on the same JSON plumbing.
5. **#13 Embedder builtins registry** — once #6 is in, custom builtins get
   audit-logged for free.

This batch is roughly 1-2 sessions and produces a meaningfully
agent-friendly shell without touching bash compatibility at all.
