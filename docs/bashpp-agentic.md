# Agentic actions in Bash++ / Bash#

`agentic` is a bare opt-in contract for LLM-assisted actions. An action takes
input and returns output or an error: it can be a function, method, shell script,
command, utility, or tool. The modifier permits assistance; it does not require
a model call or change arguments, return values, streams, or exit status.

```bash
agentic func double(n int) int {
    return n + n
}

agentic function count_lines() {
    wc -l "$1"
}

agentic {
    result := double(21)
    echo "$result"
    count_lines "$1"
}
```

Typed receiver methods use the same prefix: `agentic func (r Report) Summarize()`.
Marked shell functions use `agentic function name()` with parentheses. An entire
script opts in by putting its action body in an `agentic { ...; }` block.
Function literals use an explicit block in their body; no new literal syntax
is required. Script blocks retain shell separators before their closing brace.

A marked function or method requires an agentic caller. Calling it outside an
explicit block fails before its body executes. Passing it as a value or through
an interface retains the declaration. Ordinary helpers and closures can be
called inside a block, but their bodies run with assistance off until they enter
their own explicit block. Declaring a function inside an agentic region does not
mark it.

Trap callbacks, including signal traps, and `mapfile`/`readarray -C` callbacks
also start with assistance off. They can
use their own explicit block; their interrupted caller's scope restores afterward.

The block runs in the current shell and creates no additional variable scope.
Scope restores on return, failure, panic, and cancellation. Subshells, pipelines,
and tasks copy their current scope independently. Deferred calls retain the scope
where they were scheduled. `eval` uses its current scope; sourced scripts and new
file runs start with assistance off and can opt in using their own blocks.

Cooperating in-process tools read `interp.HandlerCtx(ctx).Agentic`. Existing
command dispatch and argv remain unchanged. External programs receive no new
environment or wire protocol. Model selection, spending, network access, retries,
and tool permissions remain implementation/runtime policy. The keyword grants no
additional permissions, implies no automatic repair, and accepts no numeric level.
`BASHY_AGENTIC` does not supply this source opt-in. Existing explicit AI commands
continue to work as ordinary commands.

Only the Bash++ dialect claims these forms. Ordinary uses such as `agentic word`,
`agentic func name`, and `agentic function name` remain commands. Use
`command agentic {` or `"agentic" {` to invoke an actual command named `agentic`
with a brace argument. The unquoted `agentic {` prefix commits to a block and
therefore requires its closing brace.

`declare -f` preserves marked shell definitions as `agentic function ...`.
`export -f` refuses marked functions: Bash's `BASH_FUNC_*` transport cannot retain
the contract. Package an exported action as a Bash++ script with an entry block.
