# bashy JSON output

bashy keeps Bash-compatible text output by default. The JSON modes below are opt-in extensions for embedders that need structured shell state without parsing `declare` or `set` text.

## `set --json`

Emits one JSON object:

```json
{"variables":[{"name":"foo","kind":"string","flags":"-","set":true,"value":"bar"}]}
```

Only set variables are included, matching `set` with no arguments. The list is sorted by variable name.

## `declare --json -p [name ...]`

With names, emits one variable object per output line. Without names, emits:

```json
{"variables":[{"name":"foo","kind":"string","flags":"-","set":true,"value":"bar"}]}
```

Variable objects include:

- `name`: variable name.
- `kind`: `string`, `indexed`, `associative`, `nameref`, or `unknown`.
- `flags`: Bash-style declaration flags, or `-` when none are set.
- `set`: whether the variable has a value.
- `exported`, `readonly`, `integer`, `local`, `uppercase`, `lowercase`, `capitalized`: attribute booleans.
- `value`: string or nameref target for scalar-like variables, object keyed by numeric index strings for indexed variables, object keyed by array key for associative variables.

## `declare --json -F [name ...]`

With names, emits one function object per output line. Without names, emits:

```json
{"functions":[{"name":"f","readonly":false,"exported":false,"line":1,"body":"f() { echo ok; }"}]}
```

Function objects include `name`, `readonly`, `exported`, and, when the function body is available, `line` and `body`.
