package interp_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// S358.4 — a PowerShell island function as a Bash# command word: argv, pipes
// and redirections, the error/status model, objects as JSON and a byte pipe
// that stays bytes, and a pipeline-reading function as a stdin filter. The
// protocol-level ports live in polyglot/powershell_command_test.go; these tests
// assert the same facts at the shell surface, with Bash# owning the workflow.

// powerShellProject lays out a project whose PATH carries the provisioned pwsh
// and the few Unix tools the pipelines feed into. The runner's environment
// holds nothing else, so the worker comes from discovery, never ambient state.
func powerShellProject(t *testing.T) (dir string, environ []string) {
	t.Helper()
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh unavailable")
	}
	dir = t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"tr", "wc", "sort", "cat", "jq"} {
		if path, err := exec.LookPath(tool); err == nil {
			_ = os.Symlink(path, filepath.Join(bin, tool))
		}
	}
	path := bin + string(os.PathListSeparator) + filepath.Dir(pwsh)
	environ = []string{"PATH=" + path, "HOME=" + dir}
	if runtime.GOOS == "windows" {
		environ = append(environ, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"), "PATHEXT="+os.Getenv("PATHEXT"))
	}
	return dir, environ
}

const powerShellCommandSource = `~~~powershell as ps
function Greet {
    param([string]$name)
    "hello $name"
}
function Shout {
    process { $_.ToUpper() }
}
function Rows {
    [pscustomobject]@{ id = 2; name = 'b' }
    [pscustomobject]@{ id = 1; name = 'a' }
}
function Fail { throw 'nope' }
function Warned { Write-Error 'soft'; 'kept going' }
function Crlf { "line1` + "`r`n" + `line2" }
function Bytes { ,[byte[]]@(104, 105, 10) }
~~~
`

func runPowerShellCommandScript(t *testing.T, dir string, environ []string, body string) (string, string, int) {
	t.Helper()
	return runBashPP(t, filepath.Join(dir, "source.bpp"), dir, environ, powerShellCommandSource+body)
}

// The command word reaches the function with argv intact and its success-stream
// output is the command's stdout; a clean run is status 0.
func TestBashPPPowerShellCommandArgvAndStatus(t *testing.T) {
	dir, environ := powerShellProject(t)
	stdout, stderr, status := runPowerShellCommandScript(t, dir, environ, `ps.Greet world
echo "status=$?"
`)
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stderr=%q stdout=%q", status, stderr, stdout)
	}
	if stdout != "hello world\nstatus=0\n" {
		t.Fatalf("stdout = %q", stdout)
	}
}

// A Unix pipeline feeds a PowerShell filter and its output feeds sort: the
// bytes are the same on every OS. Objects cross as JSON, so a pipeline can feed
// jq. The shell pipe stays bytes throughout.
func TestBashPPPowerShellCommandPipesToUnixTools(t *testing.T) {
	dir, environ := powerShellProject(t)
	if _, err := exec.LookPath("sort"); err != nil {
		t.Skip("sort unavailable")
	}
	stdout, stderr, status := runPowerShellCommandScript(t, dir, environ, `printf 'gamma\nalpha\nbeta\n' | ps.Shout | sort
`)
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stderr=%q stdout=%q", status, stderr, stdout)
	}
	if stdout != "ALPHA\nBETA\nGAMMA\n" {
		t.Fatalf("pipeline stdout = %q", stdout)
	}
	if _, err := exec.LookPath("jq"); err == nil {
		out, errText, st := runPowerShellCommandScript(t, dir, environ, `ps.Rows | jq -c 'select(.id==1)'
`)
		if st != 0 || errText != "" || out != `{"id":1,"name":"a"}`+"\n" {
			t.Fatalf("jq pipeline: status=%d stderr=%q stdout=%q", st, errText, out)
		}
	}
}

// Redirections apply because the function's streams are the command's streams: a
// non-terminating error goes to stderr while the command keeps running at 0, and
// a terminating error is the command form of the error that binds to err in a
// typed call — its message on stderr, status 1.
func TestBashPPPowerShellCommandErrorModel(t *testing.T) {
	dir, environ := powerShellProject(t)
	stdout, stderr, status := runPowerShellCommandScript(t, dir, environ, `ps.Warned > out.txt 2> err.txt
echo "warned=$?"
ps.Fail
echo "failed=$?"
`)
	if status != 0 {
		t.Fatalf("status=%d stderr=%q stdout=%q", status, stderr, stdout)
	}
	if stdout != "warned=0\nfailed=1\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "nope") {
		t.Fatalf("terminating error not on stderr: %q", stderr)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	errText, _ := os.ReadFile(filepath.Join(dir, "err.txt"))
	if string(out) != "kept going\n" {
		t.Fatalf("redirected stdout = %q", out)
	}
	if !strings.Contains(string(errText), "soft") {
		t.Fatalf("redirected stderr = %q", errText)
	}
}

// set -e honours a terminating error's status like any command's.
func TestBashPPPowerShellCommandSetE(t *testing.T) {
	dir, environ := powerShellProject(t)
	stdout, stderr, status := runPowerShellCommandScript(t, dir, environ, `set -e
ps.Fail
echo unreachable
`)
	if status != 1 {
		t.Fatalf("status=%d stderr=%q stdout=%q", status, stderr, stdout)
	}
	if strings.Contains(stdout, "unreachable") {
		t.Fatalf("set -e did not stop the script: %q", stdout)
	}
}

// Output line endings are normalised to LF so the same script yields the same
// bytes, and a byte-array output stays bytes across the pipe.
func TestBashPPPowerShellCommandBytesAndNewlines(t *testing.T) {
	dir, environ := powerShellProject(t)
	stdout, _, status := runPowerShellCommandScript(t, dir, environ, `ps.Crlf
ps.Bytes
`)
	if status != 0 {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
	if stdout != "line1\nline2\nhi\n" {
		t.Fatalf("stdout = %q", stdout)
	}
}

// A word that is not an exported function of the block is not a fence command:
// like any unknown command it is a 127 lookup failure. Classic dialects are
// untouched.
func TestBashPPPowerShellCommandLookupAndClassic(t *testing.T) {
	dir, environ := powerShellProject(t)
	stdout, stderr, status := runPowerShellCommandScript(t, dir, environ, `ps.Missing; echo "missing=$?"
`)
	if status != 0 || stdout != "missing=127\n" {
		t.Fatalf("status=%d stderr=%q stdout=%q", status, stderr, stdout)
	}
	if !strings.Contains(stderr, "ps.Missing") {
		t.Fatalf("stderr = %q", stderr)
	}
	// In a classic bash script the same word is an external lookup, no fence.
	out, errText, st := runBashPP(t, filepath.Join(dir, "source.sh"), dir, environ, "ps.Greet x; echo \"status=$?\"\n")
	if st != 0 || out != "status=127\n" {
		t.Fatalf("classic: status=%d stderr=%q stdout=%q", st, errText, out)
	}
}

// The same terminating error that a command reports as status 1 binds to err in
// a typed call; a clean typed call leaves err empty. Bash# owns the branch.
func TestBashPPPowerShellTypedCallErrorBinding(t *testing.T) {
	dir, environ := powerShellProject(t)
	stdout, stderr, status := runPowerShellCommandScript(t, dir, environ, `value, callErr := ps.Greet("x")
echo "ok=$value:${callErr:+error}"
value, callErr = ps.Fail()
echo "fail=${callErr:+error}"
[[ $callErr == *nope* ]] && echo "message=nope"
`)
	if status != 0 {
		t.Fatalf("status=%d stderr=%q stdout=%q", status, stderr, stdout)
	}
	if stdout != "ok=hello x:\nfail=error\nmessage=nope\n" {
		t.Fatalf("stdout = %q stderr=%q", stdout, stderr)
	}
}
