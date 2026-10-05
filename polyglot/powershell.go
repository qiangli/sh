package polyglot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PowerShell is a first-class, declaration-only PowerShell fence: its exported
// functions and typed param blocks become Bash# callables, like the Python and
// TypeScript rows. It is NOT a runner fence. One persistent pwsh worker serves
// a fence for the life of its Module — started with no profile and
// non-interactive, speaking JSON lines — so session state (functions, script
// variables) persists between calls and a cancellation restarts it from the
// immutable plan. The guest runtime is PowerShell 7 (Windows PowerShell 5.1 is
// out of scope); bashy provisions it on demand through the toolchain resolver
// and the BASHPP_PWSH override names one explicitly.
type PowerShell struct {
	Command     string
	Environment *EnvironmentPlan
	// Cwd, when set, answers the caller's directory at call time: each call
	// runs in the caller's directory so a relative path inside a fence means
	// what it means to the shell line that called it.
	Cwd func() string
}

func (p PowerShell) executable() string {
	if p.Environment != nil && p.Environment.Executable != "" {
		return p.Environment.Executable
	}
	if p.Command != "" {
		return p.Command
	}
	if command := os.Getenv("BASHPP_PWSH"); command != "" {
		return command
	}
	return "pwsh"
}

func (p PowerShell) name() string { return "PowerShell" }

// powerShellBaseArgs are the launch flags both the analyzer and the worker use:
// no logo, no profile, non-interactive. -Command runs the script argument while
// leaving stdin free for the source (analyzer) or the protocol (worker).
func powerShellBaseArgs() []string {
	return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}
}

func (p PowerShell) arguments(Plan) []string {
	args := append(leadingArgs(p.Environment), powerShellBaseArgs()...)
	return append(args, powershellWorker)
}

func (p PowerShell) loadRequest(plan Plan) map[string]any {
	return map[string]any{"id": 0, "op": "load", "source": plan.Source}
}

func (p PowerShell) configure(cmd *exec.Cmd) {
	if p.Environment == nil {
		return
	}
	cmd.Dir = p.Environment.Dir
	cmd.Env = append([]string(nil), p.Environment.Env...)
}

// Analyze runs pwsh with the PowerShell AST parser over the source and returns
// its exported functions. The parser itself — not a regexp — decides what a
// function and its typed param block are, so the analyzer is as precise as
// PowerShell's own language services. A parse error is a prepare-time failure
// with the fence line, matching the other compiled/checked rows.
func (p PowerShell) Analyze(ctx context.Context, source string) ([]Export, error) {
	name, argv := workerExecArgs(p.executable(), append(leadingArgs(p.Environment), append(powerShellBaseArgs(), powershellAnalyze)...))
	cmd := exec.CommandContext(ctx, name, argv...)
	p.configure(cmd)
	cmd.Stdin = strings.NewReader(source)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("PowerShell runtime unavailable: %w", err)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var exports []Export
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &exports); err != nil {
		return nil, fmt.Errorf("invalid PowerShell analyzer response: %w", err)
	}
	if len(exports) == 0 {
		return nil, errors.New("no PowerShell functions to export")
	}
	return exports, nil
}

// powershellAnalyze reads the fence source from stdin, parses it with the
// PowerShell language parser, and writes the exported functions as a JSON array
// (the Export shape) to stdout. A top-level function whose name does not start
// with "_" is exported; its declared parameter types and any [OutputType(...)]
// map to the Bash# boundary kinds. The JSON is built by hand so a single-item
// params/results array is never collapsed to a scalar.
const powershellAnalyze = `
$ErrorActionPreference = 'Stop'
$src = [Console]::In.ReadToEnd()
$tokens = $null
$perrors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseInput($src, [ref]$tokens, [ref]$perrors)
if ($perrors -and $perrors.Count -gt 0) {
    $m = ($perrors | ForEach-Object { '<bash++ powershell>:' + $_.Extent.StartLineNumber + ': ' + $_.Message }) -join "` + "`n" + `"
    [Console]::Error.Write($m)
    exit 1
}
function MapType($t) {
    if ($null -eq $t) { return 'any' }
    switch ($t.FullName) {
        'System.String' { return 'string' }
        'System.Char' { return 'string' }
        'System.Boolean' { return 'bool' }
        'System.Management.Automation.SwitchParameter' { return 'bool' }
        'System.Int16' { return 'int' }
        'System.Int32' { return 'int' }
        'System.Int64' { return 'int' }
        'System.Byte' { return 'int' }
        'System.SByte' { return 'int' }
        'System.UInt16' { return 'int' }
        'System.UInt32' { return 'int' }
        'System.UInt64' { return 'int' }
        'System.Numerics.BigInteger' { return 'int' }
        'System.Single' { return 'float64' }
        'System.Double' { return 'float64' }
        'System.Decimal' { return 'float64' }
        'System.Byte[]' { return 'bytes' }
        'System.Void' { return 'nil' }
        'System.Object' { return 'any' }
        default { return 'object' }
    }
}
function JStr($s) { return (ConvertTo-Json -InputObject ([string]$s) -Compress) }
$funcs = $ast.FindAll({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] }, $false)
$seen = @{}
$parts = @()
foreach ($f in $funcs) {
    $fname = $f.Name
    if ($fname.StartsWith('_')) { continue }
    if ($seen.ContainsKey($fname)) { [Console]::Error.Write('PowerShell function ' + $fname + ' is defined more than once'); exit 1 }
    $seen[$fname] = $true
    $dynamic = $false
    $pl = $null
    if ($f.Parameters) { $pl = $f.Parameters }
    elseif ($f.Body.ParamBlock) { $pl = $f.Body.ParamBlock.Parameters }
    $params = @()
    if ($pl) {
        foreach ($p in $pl) {
            $kind = MapType $p.StaticType
            if ($kind -eq 'any') { $dynamic = $true }
            $params += $kind
        }
    }
    $ot = $null
    if ($f.Body.ParamBlock) {
        foreach ($attr in $f.Body.ParamBlock.Attributes) {
            if ($attr.TypeName.Name -eq 'OutputType') {
                foreach ($pa in $attr.PositionalArguments) {
                    if ($pa -is [System.Management.Automation.Language.TypeExpressionAst]) {
                        try { $ot = $pa.TypeName.GetReflectionType() } catch { $ot = $null }
                    }
                }
            }
        }
    }
    # Without [OutputType(...)] a PowerShell function's result kind is unknown,
    # so the export is dynamic and its value crosses untyped-but-typed.
    $results = @('any')
    if ($ot) {
        $rk = MapType $ot
        if ($rk -eq 'nil') { $results = @() }
        elseif ($rk -eq 'any') { $results = @('any'); $dynamic = $true }
        else { $results = @($rk) }
    } else {
        $dynamic = $true
    }
    $pstr = '[' + (($params | ForEach-Object { JStr $_ }) -join ',') + ']'
    $rstr = '[' + (($results | ForEach-Object { JStr $_ }) -join ',') + ']'
    $d = 'false'
    if ($dynamic) { $d = 'true' }
    $parts += '{"name":' + (JStr $fname) + ',"signature":{"params":' + $pstr + ',"results":' + $rstr + ',"dynamic":' + $d + '}}'
}
[Console]::Out.Write('[' + ($parts -join ',') + ']')
`

// powershellWorker is the persistent pwsh worker Module.ensure launches. It
// reads one JSON request per stdin line and writes one JSON response per line
// on fd 3 (Unix) or on the inherited stdout handle behind the "\x1eBASHPP"
// marker (Windows, where the parent cannot pass a fourth file). The source's
// functions are dot-sourced into the session on load, so static script state
// persists between calls. Each call captures the function's information stream
// (Write-Host) as stdout and its error stream (Write-Error) as stderr, so the
// protocol stream stays clean and the success stream is the typed return value.
//
// Values cross the boundary as JSON: int/float/bool/string pass as scalars,
// bytes as {"$bytes": base64}, a list/array as a JSON array and a
// hashtable/pscustomobject as a JSON object (both ways), and any other .NET
// object as an opaque {"$handle": ...} the worker owns until release.
const powershellWorker = `
$stdinReader = New-Object System.IO.StreamReader([Console]::OpenStandardInput(), (New-Object System.Text.UTF8Encoding($false)))
if ($IsWindows) {
    $rawOut = [Console]::OpenStandardOutput()
} else {
    $sh = New-Object Microsoft.Win32.SafeHandles.SafeFileHandle([IntPtr]3, $false)
    $rawOut = New-Object System.IO.FileStream($sh, [System.IO.FileAccess]::Write)
}
$protocol = New-Object System.IO.StreamWriter($rawOut, (New-Object System.Text.UTF8Encoding($false)))
$protocol.AutoFlush = $true
$script:handles = @{}
$script:nextHandle = 0

function Write-Protocol($obj) {
    $json = ConvertTo-Json -InputObject $obj -Depth 64 -Compress
    $protocol.Write("` + "`u{1e}" + `BASHPP")
    $protocol.Write($json)
    $protocol.Write("` + "`n" + `")
}
function Dec($v) {
    if ($v -is [System.Collections.IDictionary]) {
        $keys = @($v.Keys)
        if ($keys.Count -eq 1 -and $keys[0] -eq '$bytes') { return ,[System.Convert]::FromBase64String([string]$v['$bytes']) }
        if ($keys.Count -eq 1 -and $keys[0] -eq '$handle') {
            $id = [int64]$v['$handle']
            if (-not $script:handles.ContainsKey($id)) { throw ('stale PowerShell handle ' + $id) }
            return $script:handles[$id]
        }
        $h = @{}
        foreach ($k in $v.Keys) { $h[[string]$k] = Dec $v[$k] }
        return $h
    }
    if ($v -is [System.Collections.IEnumerable] -and -not ($v -is [string])) {
        return ,@($v | ForEach-Object { Dec $_ })
    }
    return $v
}
function Enc($v) {
    if ($null -eq $v) { return $null }
    if ($v -is [bool]) { return $v }
    if ($v -is [byte[]]) { return @{ '$bytes' = [System.Convert]::ToBase64String($v) } }
    if ($v -is [string]) { return $v }
    if ($v -is [char]) { return [string]$v }
    if ($v -is [sbyte] -or $v -is [byte] -or $v -is [int16] -or $v -is [uint16] -or $v -is [int32] -or $v -is [uint32] -or $v -is [int64] -or $v -is [uint64] -or $v -is [System.Numerics.BigInteger]) { return $v }
    if ($v -is [single] -or $v -is [double] -or $v -is [decimal]) { return $v }
    if ($v -is [System.Collections.IDictionary]) {
        $h = [ordered]@{}
        foreach ($k in $v.Keys) { $h[[string]$k] = Enc $v[$k] }
        return $h
    }
    if ($v -is [System.Management.Automation.PSCustomObject] -or $v -is [psobject]) {
        $h = [ordered]@{}
        foreach ($p in $v.PSObject.Properties) { $h[$p.Name] = Enc $p.Value }
        return $h
    }
    if ($v -is [System.Collections.IEnumerable] -and -not ($v -is [string])) {
        $a = @()
        foreach ($item in $v) { $a += ,(Enc $item) }
        return ,$a
    }
    $script:nextHandle++
    $id = $script:nextHandle
    $script:handles[$id] = $v
    $repr = ''
    try { $repr = [string]$v } catch { $repr = '<unrepresentable>' }
    return @{ '$handle' = @{ id = $id; type = $v.GetType().Name; repr = $repr; callable = $false } }
}
function Envelope-Error($errRecord) {
    $ex = $errRecord
    if ($errRecord -is [System.Management.Automation.ErrorRecord]) { $ex = $errRecord.Exception }
    $code = 'PowerShellError'
    $message = [string]$errRecord
    $help = ''
    if ($ex) {
        $code = $ex.GetType().Name
        if ($ex.Message) { $message = $ex.Message }
    }
    if ($errRecord -is [System.Management.Automation.ErrorRecord]) {
        $help = [string]$errRecord.ScriptStackTrace
        if ($errRecord.FullyQualifiedErrorId) { $code = [string]$errRecord.FullyQualifiedErrorId }
        if ($ex) { $code = $ex.GetType().Name }
    }
    return @{ code = $code; message = $message; help = $help }
}
# Read-CaptureText reads a capture file and removes it; a missing or unreadable
# file reads as empty. The call sites redirect the island's information stream
# (Write-Host) and error stream (Write-Error) into these files inline, at script
# scope, so the invoked function and its arguments resolve with ordinary scope
# rules — no nested-function or closure scope to cross.
function Read-CaptureText($path) {
    $text = ''
    try { $text = [System.IO.File]::ReadAllText($path) } catch {}
    Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
    return $text
}

while ($true) {
    $line = $stdinReader.ReadLine()
    if ($null -eq $line) { break }
    if ($line.Trim() -eq '') { continue }
    $rid = 0
    $res = $null
    try {
        $req = ConvertFrom-Json -InputObject $line -AsHashtable
        if ($req.ContainsKey('id')) { $rid = [int64]$req['id'] }
        $op = [string]$req['op']
        if ($req.ContainsKey('cwd') -and $req['cwd'] -and $op -ne 'job_join' -and $op -ne 'job_leave') {
            Set-Location -LiteralPath ([string]$req['cwd'])
        }
        switch ($op) {
            'job_join' { $res = @{ id = $rid; ok = $true; result = $PID; stdout = ''; stderr = '' } }
            'job_leave' { $res = @{ id = $rid; ok = $true; result = $PID; stdout = ''; stderr = '' } }
            'load' {
                $src = [string]$req['source']
                $block = [scriptblock]::Create($src)
                $outFile = New-TemporaryFile
                $o = ''
                try {
                    . $block *> $outFile.FullName
                    try { $o = [System.IO.File]::ReadAllText($outFile.FullName) } catch {}
                } finally {
                    Remove-Item -LiteralPath $outFile.FullName -Force -ErrorAction SilentlyContinue
                }
                $res = @{ id = $rid; ok = $true; result = $null; stdout = $o; stderr = '' }
            }
            'call' {
                $fname = [string]$req['name']
                $argv = @()
                if ($req.ContainsKey('args') -and $null -ne $req['args']) {
                    foreach ($a in @($req['args'])) { $argv += ,(Dec $a) }
                }
                $kw = @{}
                if ($req.ContainsKey('kwargs') -and $req['kwargs'] -is [System.Collections.IDictionary]) {
                    foreach ($k in $req['kwargs'].Keys) { $kw[[string]$k] = Dec $req['kwargs'][$k] }
                }
                $cmd = Get-Command -Name $fname -CommandType Function -ErrorAction SilentlyContinue
                if ($null -eq $cmd) { throw [System.Management.Automation.ItemNotFoundException]::new('no PowerShell function ' + $fname) }
                $outFile = New-TemporaryFile
                $errFile = New-TemporaryFile
                $value = $null
                $failure = $null
                try {
                    $value = & $fname @argv @kw 6> $outFile.FullName 2> $errFile.FullName
                } catch {
                    $failure = $_
                }
                $o = Read-CaptureText $outFile.FullName
                $e = Read-CaptureText $errFile.FullName
                if ($failure) {
                    $res = @{ id = $rid; ok = $false; error = (Envelope-Error $failure); stdout = $o; stderr = $e }
                } else {
                    $res = @{ id = $rid; ok = $true; result = (Enc $value); stdout = $o; stderr = $e }
                }
            }
            'getattr' {
                $name = [string]$req['name']
                $target = $null
                if ($req.ContainsKey('handle')) {
                    $hid = [int64]$req['handle']
                    if (-not $script:handles.ContainsKey($hid)) { throw ('stale PowerShell handle ' + $hid) }
                    $target = $script:handles[$hid]
                }
                $outFile = New-TemporaryFile
                $errFile = New-TemporaryFile
                $value = $null
                $failure = $null
                try {
                    $value = $target.$name 6> $outFile.FullName 2> $errFile.FullName
                } catch {
                    $failure = $_
                }
                $o = Read-CaptureText $outFile.FullName
                $e = Read-CaptureText $errFile.FullName
                if ($failure) {
                    $res = @{ id = $rid; ok = $false; error = (Envelope-Error $failure); stdout = $o; stderr = $e }
                } else {
                    $res = @{ id = $rid; ok = $true; result = (Enc $value); stdout = $o; stderr = $e }
                }
            }
            'release' {
                if ($req.ContainsKey('handle')) { $script:handles.Remove([int64]$req['handle']) | Out-Null }
                $res = @{ id = $rid; ok = $true; result = $null; stdout = ''; stderr = '' }
            }
            default { throw ('unknown operation ' + $op) }
        }
    } catch {
        $res = @{ id = $rid; ok = $false; error = (Envelope-Error $_); stdout = ''; stderr = '' }
    }
    Write-Protocol $res
}
`
