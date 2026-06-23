# BRACES-BLOCKERS.md

## brace-expansion__011.sh

Script:

```sh
a=A
echo {$a,b}_{c,d}
```

Bash 5.3 output:

```text
b_c b_d
```

Diagnosis: `expand/braces.go` generates the correct textual brace results:
`$a_c`, `$a_d`, `b_c`, and `b_d`. The remaining mismatch happens later, after
`expand.mergeIdentAfterParamExp` folds `$a` plus `_c`/`_d` into unbraced
parameter expansions for `a_c` and `a_d`. Those unset parameters currently
survive as empty unquoted fields, so `gosh` passes two empty arguments to
`echo` before `b_c b_d`. Bash elides those empty fields. Fixing that requires a
change in the field-building/parameter-expansion path outside `expand/braces.go`.

Verified local behavior:

```text
  b_c b_d
```

## brace-expansion__042.sh

Script:

```sh
case $SH in *zsh) echo BUG; exit ;; esac
echo -{z..A}-
echo -{z..A..2}-
```

Bash 5.3 reports command-substitution parse errors after textual brace
expansion reaches the backtick character:

```text
./s: line 2: bad substitution: no closing "`" in `-
./s: line 3: bad substitution: no closing "`" in `-
```

Diagnosis: this repository parses the script into an AST before
`expand.BracesSeq` runs. The `{z..A}` sequence is represented as a
`syntax.BraceExp`, and the generated backtick is emitted as a literal word part,
so later expansion phases do not reinterpret it as shell syntax. Matching bash
requires a cross-file change at the parser/expansion boundary, such as reparsing
textual brace results or teaching the expansion pipeline to surface this bash
command-substitution error when generated text introduces backticks.

Verified local behavior:

```text
-z- -y- -x- -w- -v- -u- -t- -s- -r- -q- -p- -o- -n- -m- -l- -k- -j- -i- -h- -g- -f- -e- -d- -c- -b- -a- -`- -_- -^- -]- -- -[- -Z- -Y- -X- -W- -V- -U- -T- -S- -R- -Q- -P- -O- -N- -M- -L- -K- -J- -I- -H- -G- -F- -E- -D- -C- -B- -A-
-z- -x- -v- -t- -r- -p- -n- -l- -j- -h- -f- -d- -b- -`- -^- -- -Z- -X- -V- -T- -R- -P- -N- -L- -J- -H- -F- -D- -B-
```
