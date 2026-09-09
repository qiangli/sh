# GoSource process environment

Sprint: #118; Story: #1; Story-ID: `2daf9ef04ad4`.

The native dependency process previously received every value from the shell's
writable variable overlay. That exposed synthesized shell state such as UID,
IFS and OPTIND to ordinary Go `os.Environ`, including non-exported values.

`interp.GoSourceEnv([]string)` supplies a copied, ordered starting environment
for GoSource dependency processes. No variable-name filtering occurs; explicitly
supplied BASH, SHELL, SHLVL and BASHY_AGENT_MANIFEST values remain unchanged.
An empty or nil slice explicitly means empty, never host-process inheritance.
Without this option, exported initial `Runner.Env` entries are used in that
environment's iteration order. `expand.ListEnviron` sorts its input; callers
requiring original process order should pass the original slice with this option.

Reset retains the immutable configuration and starts the next Go dependency
session from it. Public subshells copy the configuration. Calls to Go
`os.Setenv`, `os.Unsetenv` and `os.Clearenv` mutate the live dependency process;
subprocesses launched by that process see those changes. They do not mutate the
embedding process or the next session's starting environment. SDK/build helper
configuration remains separate. Ordinary shell variables and shell subprocess
environments retain their existing behavior.

The tests build/run the original Go by Example environment program and compare
raw output with Runner for an empty environment, absent shell variables and
deliberately supplied shell/agent variables. The fixture bytes are unchanged:
SHA-256 `1c8d6019811e77ed15c09a95913e3e60361382c6a8255af2c7a7c4ce3c522f21`.
They also exercise caller-slice mutation, Reset, persistent Go environment
mutation, native subprocess inheritance, exported-only defaults and ordinary
Bash export isolation. Native session/bridge regressions pass alongside them.

```sh
GOMAXPROCS=2 go test -p 2 ./interp -run 'TestGoSource.*Environment|TestRunnerResetFields|TestGoSourceNativeSessionLifecycle|TestGoSourcePersistentNativeBridge' -count=1
```

Measured result: PASS (48.5 s). The required external Bash comparison is also
run separately; its existing external-shell fixture mismatches are not treated
as a passing regression or evidence of this GoSource behavior.
