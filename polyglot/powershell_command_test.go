package polyglot

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// S358.4 — a PowerShell function as a shell command. The command form renders
// the success stream to stdout (string as text, object as JSON, bytes as
// bytes), routes non-terminating errors and warnings to stderr, binds a
// terminating error to stderr with status 1, normalises line endings, and runs
// a pipeline-reading function as a stdin filter.
const powerShellCommandFixture = `
function Hello { 'hello' }
function Echo {
    param([string]$a, [string]$b)
    "$a $b"
}
function Lines { 'b'; 'a'; 'c' }
function AsObject { [pscustomobject]@{ name = 'x'; n = 2 } }
function AsNumber { 42 }
function AsBytes { ,[byte[]]@(104, 105) }
function Warn { Write-Warning 'careful'; 'ok' }
function NonTerm { Write-Error 'bad thing'; 'after' }
function Boom { throw 'kaboom' }
function CrlfText { "one` + "`r`n" + `two" }
$script:seen = 0
function Bump { $script:seen++; $script:seen }
function Upper {
    process { $_.ToUpper() }
}
function CountLines {
    $n = 0
    $input | ForEach-Object { $n++ }
    $n
}
function _hidden { 'secret' }
function RawBytes { ,[byte[]]@(0xff, 0x00, 0x0d, 0x0a, 0x80) }
function NativeExit {
    param([string]$code)
    & ([System.Diagnostics.Process]::GetCurrentProcess().MainModule.FileName) -NoProfile -NonInteractive -Command "exit $code"
    'ran'
}
`

func powerShellCommandModule(t *testing.T) *Module {
	t.Helper()
	plan, runtime := powerShellPlan(t, powerShellCommandFixture)
	m := Start(plan, runtime)
	t.Cleanup(func() { m.Close() })
	return m
}

func runPowerShellCommand(t *testing.T, m *Module, name string, argv ...string) CommandResult {
	t.Helper()
	result, err := m.Command(context.Background(), name, argv, nil)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

// Argv reaches the function word for word; its success stream renders to
// stdout: a string is text, an object and a number cross as JSON, a byte array
// stays bytes. A clean command reports status 0.
func TestPowerShellCommandArgvAndRendering(t *testing.T) {
	m := powerShellCommandModule(t)
	if got := runPowerShellCommand(t, m, "Hello"); got.Status != 0 || got.Stdout != "hello\n" || got.Stderr != "" {
		t.Fatalf("Hello = %#v", got)
	}
	if got := runPowerShellCommand(t, m, "Echo", "a b", "c"); got.Stdout != "a b c\n" {
		t.Fatalf("Echo = %#v", got)
	}
	if got := runPowerShellCommand(t, m, "Lines"); got.Stdout != "b\na\nc\n" {
		t.Fatalf("Lines = %#v", got)
	}
	if got := runPowerShellCommand(t, m, "AsObject"); got.Stdout != `{"name":"x","n":2}`+"\n" {
		t.Fatalf("AsObject = %#v", got)
	}
	if got := runPowerShellCommand(t, m, "AsNumber"); got.Stdout != "42\n" {
		t.Fatalf("AsNumber = %#v", got)
	}
	if got := runPowerShellCommand(t, m, "AsBytes"); got.Stdout != "hi" || got.Status != 0 {
		t.Fatalf("AsBytes = %#v", got)
	}
}

// Output line endings are normalised to LF so the same script gives the same
// bytes on every OS, even when the function emits CRLF text.
func TestPowerShellCommandNormalisesNewlines(t *testing.T) {
	m := powerShellCommandModule(t)
	if got := runPowerShellCommand(t, m, "CrlfText"); got.Stdout != "one\ntwo\n" {
		t.Fatalf("CrlfText = %q", got.Stdout)
	}
}

// A non-terminating error (Write-Error) and a warning go to stderr while the
// command keeps running and reports 0; a terminating error binds to stderr with
// status 1 and the worker keeps serving afterwards.
func TestPowerShellCommandErrorModel(t *testing.T) {
	m := powerShellCommandModule(t)
	got := runPowerShellCommand(t, m, "NonTerm")
	if got.Status != 0 || got.Stdout != "after\n" || !strings.Contains(got.Stderr, "bad thing") {
		t.Fatalf("NonTerm = %#v", got)
	}
	warn := runPowerShellCommand(t, m, "Warn")
	if warn.Status != 0 || warn.Stdout != "ok\n" || !strings.Contains(warn.Stderr, "careful") {
		t.Fatalf("Warn = %#v", warn)
	}
	boom := runPowerShellCommand(t, m, "Boom")
	if boom.Status != 1 || boom.Stdout != "" || !strings.Contains(boom.Stderr, "kaboom") {
		t.Fatalf("Boom = %#v", boom)
	}
	// The worker survives a terminating error.
	if got := runPowerShellCommand(t, m, "Hello"); got.Stdout != "hello\n" {
		t.Fatalf("after Boom, Hello = %#v", got)
	}
}

// Module state persists across commands: the worker is one process and a
// command is one call into it.
func TestPowerShellCommandKeepsModuleState(t *testing.T) {
	m := powerShellCommandModule(t)
	for _, want := range []string{"1\n", "2\n", "3\n"} {
		if got := runPowerShellCommand(t, m, "Bump"); got.Stdout != want {
			t.Fatalf("Bump = %#v, want %q", got, want)
		}
	}
}

// A function that reads pipeline input runs as a filter over the command's
// stdin; one that does not is left alone (and never reads a reader it was not
// given).
func TestPowerShellCommandStdinFilter(t *testing.T) {
	m := powerShellCommandModule(t)
	upper, err := m.Command(context.Background(), "Upper", nil, strings.NewReader("foo\nbar\n"))
	if err != nil || upper.Status != 0 || upper.Stdout != "FOO\nBAR\n" {
		t.Fatalf("Upper = %#v, %v", upper, err)
	}
	count, err := m.Command(context.Background(), "CountLines", nil, strings.NewReader("one\ntwo\nthree\n"))
	if err != nil || count.Stdout != "3\n" {
		t.Fatalf("CountLines = %#v, %v", count, err)
	}
	// CRLF upstream is still line-oriented input to the filter.
	crlf, err := m.Command(context.Background(), "CountLines", nil, strings.NewReader("a\r\nb\r\n"))
	if err != nil || crlf.Stdout != "2\n" {
		t.Fatalf("CountLines CRLF = %#v, %v", crlf, err)
	}
}

// The analyzer marks exactly the pipeline-reading functions, so the command
// adapter knows which functions to feed stdin.
func TestPowerShellCommandPipeInputSignature(t *testing.T) {
	m := powerShellCommandModule(t)
	pipe := map[string]bool{}
	for _, export := range m.Plan().Exports {
		pipe[export.Name] = export.Signature.PipeInput
	}
	for _, name := range []string{"Upper", "CountLines"} {
		if !pipe[name] {
			t.Errorf("%s should be marked as reading pipeline input", name)
		}
	}
	for _, name := range []string{"Hello", "Echo", "Lines"} {
		if pipe[name] {
			t.Errorf("%s should not be marked as reading pipeline input", name)
		}
	}
}

// A missing function is the shell's command-not-found; a private name is hidden.
func TestPowerShellCommandLookupFailures(t *testing.T) {
	m := powerShellCommandModule(t)
	if _, err := m.Command(context.Background(), "Nope", nil, nil); !errors.Is(err, ErrCommandNotFound) {
		t.Fatalf("missing = %v", err)
	}
	if _, err := m.Command(context.Background(), "_hidden", nil, nil); !errors.Is(err, ErrCommandNotFound) {
		t.Fatalf("private = %v", err)
	}
}

// A byte array is written to the pipe byte for byte: no re-encoding, no newline
// normalisation, no trailing newline — even when the bytes are not UTF-8.
func TestPowerShellCommandRawBytes(t *testing.T) {
	m := powerShellCommandModule(t)
	if got := runPowerShellCommand(t, m, "RawBytes"); got.Status != 0 || got.Stdout != "\xff\x00\r\n\x80" {
		t.Fatalf("RawBytes = %q status=%d", got.Stdout, got.Status)
	}
}

// A native child's exit code is the command status, and it never leaks into the
// next command: each command starts from a clean $LASTEXITCODE.
func TestPowerShellCommandNativeExitStatus(t *testing.T) {
	m := powerShellCommandModule(t)
	if got := runPowerShellCommand(t, m, "NativeExit", "3"); got.Status != 3 || got.Stdout != "ran\n" {
		t.Fatalf("NativeExit 3 = %#v", got)
	}
	if got := runPowerShellCommand(t, m, "Hello"); got.Status != 0 {
		t.Fatalf("status leaked into Hello = %#v", got)
	}
	if got := runPowerShellCommand(t, m, "NativeExit", "0"); got.Status != 0 {
		t.Fatalf("NativeExit 0 = %#v", got)
	}
}
