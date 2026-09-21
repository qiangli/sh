# Bash# explicit structured capture

`run` and `capture` preserve process output as bytes represented by a Bash#
string. They do not inspect stdout, infer JSON, or turn a pipe into a typed
pipeline. Use `json.Decode` when—and only when—the receiving code intends to
cross the output-conversion boundary into an `Object`.

This uses the Sprint 216 adapter terminology and order:

`bind/validate input → convert input → invoke Bash target → convert output → contracts/attestation`

For a process result, `run` performs the invocation and preserves its three
separate channels: `Stdout`, `Stderr`, and `Status`. `json.Decode(r.Stdout)`
is an explicit **convert output** step after that invocation. It is not a new
pipe type or an implicit adapter. Classic pipes remain byte streams, and their
readers receive exactly those bytes unless they explicitly call `json.Decode`.

## Runnable example

Run the example as its executable gate:

```sh
go test ./interp -run TestBashPPStructuredCaptureExample
```

The fixture executes this Bash# program:

```bash
import "encoding/json"

report() { printf '{"name":"Ada","tags":["go","shell"]}'; }
result, rerr := run(report)
value, derr := json.Decode(result.Stdout)
printf '%s:%s\n' value.name value.tags[1]
# Ada:shell
```

Check both result errors before using the value in production code. A failed
decode binds an empty value and a non-empty `derr`, so it cannot look like a
successful decode of `""`.

## Decode contract

`json.Decode` accepts exactly one complete JSON value, followed only by JSON
whitespace. Objects, lists, and `null` become Bash# `Object` values. Strings,
numbers (with their source spelling), and booleans remain scalar strings.

Empty input, malformed JSON, and trailing non-whitespace data produce a
diagnostic. Duplicate object keys are accepted with **last key wins**, matching
Go's `encoding/json` map decode; this is intentional policy, not an implicit
validation pass.

The boundary fixtures faithfully port CPython's exact `{}`, `[]`, `""`, and
`[1, 2, 3]5` inputs and their success/error assertions, independently
expressed in Bash# rather than copying the upstream test implementation. The
source is CPython v3.14.0 commit
[`ebf955df7a89ed0c7968f79faec1de49f61ed7cb`](https://github.com/python/cpython/commit/ebf955df7a89ed0c7968f79faec1de49f61ed7cb):
[`Lib/test/test_json/test_decode.py`](https://github.com/python/cpython/blob/ebf955df7a89ed0c7968f79faec1de49f61ed7cb/Lib/test/test_json/test_decode.py),
`TestDecode.test_empty_objects` and `TestDecode.test_extra_data`. CPython is
licensed under the [Python Software Foundation License Version 2](https://github.com/python/cpython/blob/ebf955df7a89ed0c7968f79faec1de49f61ed7cb/LICENSE).

Focused fixture coverage lives in `interp/TestBashPPDecode*` and covers object,
list, scalar, empty input, malformed JSON, duplicate keys, and trailing data.
