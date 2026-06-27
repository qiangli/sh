# builtins-p.tst — deferred cases (pure-Go ceiling)

These yash `builtins-p.tst` cases cannot be matched to GNU bash 5.3 by any
change confined to `interp/` or `expand/`. All other cases in the file already
pass (verified byte-for-byte against bash 5.3 stdout/exit).

## `intrinsic built-in read can be invoked without $PATH`

```
read a
_this_line_is_read_by_the_read_built_in_
```
Expected: exit 0, empty stdout, empty stderr (the data line is consumed by `read`).
gosh gives exit 127 with `_this_line_…: command not found`.

Reason: when the script is piped on stdin, `cmd/gosh` does `io.ReadAll(os.Stdin)`
and the streaming `syntax` parser over-reads the pipe — so by the time the `read`
builtin reads stdin, the following physical line has already been consumed by the
parser and is then executed as its own command. Real bash reads a piped script
lazily so `read` and the parser share the same fd position. This is a
`cmd/gosh` input-buffering + parser read-ahead constraint, not a builtin bug;
it cannot be fixed in `interp/`/`expand/`.
