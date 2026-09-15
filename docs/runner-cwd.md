# Runner-local working directories

`interp.Runner` owns a working directory as interpreter state. `interp.Dir`
initializes that state and shell `cd` updates it; neither operation calls
`os.Chdir`, so the host process's working directory is unaffected.

A foreground `cd` deliberately persists: it updates `Runner.Dir`, `PWD`,
`OLDPWD`, and the directory stack for later commands on that runner. A shell
copy has independent state, so `cd` in an asynchronous list, subshell, or
Bash++ `go` task can safely affect that copy without changing its parent.
`awd` uses this property to make a wrapped directory change temporary.

This isolation is for copies created by the interpreter. A caller must not
invoke `Run` concurrently with `Run` or `Subshell` on the same `*Runner`;
that remains outside the API contract. Create independent runners, or serialize
those caller-driven operations.
