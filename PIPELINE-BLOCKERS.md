# pipeline-p.tst — deferred cases

Base: bf858baf. Harness: `printf '%s' "<script>" | /tmp/gosh --posix`, compared
byte-for-byte against GNU bash 5.3.

11 of the 12 `pipeline-p.tst` cases already match bash exactly (2-/3-command
pipelines, linebreak-after-`|`, pipefail / non-pipefail exit-status tables,
negated pipelines, `false | set -o pipefail`, stderr-not-modified, compound
commands in a pipeline, redirection-overrides-pipeline). No interp/expand change
was needed for those.

## Deferred

### `stdin for first command & stdout for last are not modified`

```
cat | tail -n 1
foo
bar
__IN__
bar
__OUT__
```

Expected: `cat` (the first pipeline element) inherits the shell's own stdin and
consumes the *unread remainder of the script* (`foo\nbar`); `tail -n 1` then
prints `bar`. `foo`/`bar` are never executed as commands.

gosh prints nothing instead.

**Root cause — out of interp/expand scope.** `cmd/gosh/main.go` reads the whole
script up front with `io.ReadAll(os.Stdin)` before parsing/executing it, so by
the time `cat` runs, fd 0 is already at EOF. The same effect is visible without
a pipeline:

```
$ printf 'read x; echo "got=[$x]"; cat\nLINE2\nLINE3\n' | gosh --posix
got=[]
bash: line 2: LINE2: command not found
bash: line 3: LINE3: command not found
```

`read x` should have read `LINE2` and `cat` should have read `LINE3`; instead
gosh consumed everything and parsed the trailing data lines as commands.

This is purely a CLI input-streaming concern in `cmd/gosh`, not interpreter or
expansion semantics: the interp `Runner`'s stdin is whatever fd gosh hands it,
and the bytes are already gone. A real fix means having gosh feed the script and
the runtime stdin from the *same* fd and read the script lazily — and even then
the streaming `syntax` parser buffers internally past the current statement
(README §Caveats), so the trailing input would still be swallowed. Fixing it
correctly is a non-trivial `cmd/gosh` change, outside the `interp/`/`expand/`
scope of this task. Deferred.
