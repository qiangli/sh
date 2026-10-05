package polyglot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
	if p.Environment != nil {
		cmd.Dir = p.Environment.Dir
		cmd.Env = append([]string(nil), p.Environment.Env...)
	}
	// Minimal Linux hosts and the FROM-scratch image can have no ICU. Keep the
	// managed fence worker aligned with bashy's pwsh child environment; callers
	// with ICU can explicitly opt into culture data by setting this variable.
	if runtime.GOOS == "linux" {
		env := cmd.Env
		if env == nil {
			env = os.Environ()
		}
		found := false
		for _, item := range env {
			name, _, ok := strings.Cut(item, "=")
			if ok && strings.EqualFold(name, "DOTNET_SYSTEM_GLOBALIZATION_INVARIANT") {
				found = true
				break
			}
		}
		if !found {
			cmd.Env = append(env, "DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1")
		}
	}
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
    # A function reads the command's stdin when it runs as a shell command if it
    # has a process block, references $input, or takes a ValueFromPipeline
    # parameter. The command adapter feeds the byte pipe only to such functions.
    $pipeInput = $false
    if ($f.Body.ProcessBlock) { $pipeInput = $true }
    if (-not $pipeInput) {
        $inputRefs = $f.Body.FindAll({ param($n) $n -is [System.Management.Automation.Language.VariableExpressionAst] -and $n.VariablePath.UserPath -eq 'input' }, $true)
        if ($inputRefs -and $inputRefs.Count -gt 0) { $pipeInput = $true }
    }
    if (-not $pipeInput -and $pl) {
        foreach ($p in $pl) {
            foreach ($attr in $p.Attributes) {
                if ($attr -is [System.Management.Automation.Language.AttributeAst] -and $attr.TypeName.Name -eq 'Parameter') {
                    foreach ($na in $attr.NamedArguments) {
                        if ($na.ArgumentName -eq 'ValueFromPipeline' -or $na.ArgumentName -eq 'ValueFromPipelineByPropertyName') {
                            if ($na.ExpressionOmitted -or ($na.Argument.Extent.Text -match 'true')) { $pipeInput = $true }
                        }
                    }
                }
            }
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
    $pi = ''
    if ($pipeInput) { $pi = ',"pipe_input":true' }
    $parts += '{"name":' + (JStr $fname) + ',"signature":{"params":' + $pstr + ',"results":' + $rstr + ',"dynamic":' + $d + $pi + '}}'
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
const powershellWorker = powershellWorkerPrelude + powershellWorkerLoop

// powershellWorkerPrelude is the protocol, value-codec and capture helpers the
// PowerShell worker loop and the C# worker loop share.
const powershellWorkerPrelude = `
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
$script:fenceAssembly = $null

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
    # A C# fence's own types (a POCO or an enum declared in the fence) cross
    # as data, like a pscustomobject: public properties and fields become a
    # dict and an enum its name. Every other .NET object stays a handle.
    if ($null -ne $script:fenceAssembly -and $v.GetType().Assembly -eq $script:fenceAssembly) {
        if ($v -is [enum]) { return $v.ToString() }
        $h = [ordered]@{}
        foreach ($p in $v.PSObject.Properties) { $h[$p.Name] = Enc $p.Value }
        return $h
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
# Normalize-Newlines makes a command's stdout and stderr carry LF on every host,
# so the same workflow emits the same bytes on Windows, Linux and macOS.
function Normalize-Newlines($text) {
    if ($null -eq $text) { return '' }
    return (([string]$text) -replace "` + "`r`n" + `", "` + "`n" + `") -replace "` + "`r" + `", "` + "`n" + `"
}
# Render-CommandOutput turns a function's success stream into the command's
# stdout bytes: a string is UTF-8 text with LF line endings, a byte array passes
# through unchanged (no newline added, no re-encoding), an information record
# (Write-Host) is its message, and any other object crosses as compact JSON —
# the shell pipe stays bytes and no object pipeline is added.
function Render-CommandOutput($value) {
    $ms = New-Object System.IO.MemoryStream
    $utf8 = New-Object System.Text.UTF8Encoding($false)
    $items = @($value)
    # A single byte-array output is one item: @() would enumerate it to integers.
    if ($value -is [byte[]]) { $items = @(,$value) }
    foreach ($item in $items) {
        if ($null -eq $item) { continue }
        if ($item -is [byte[]]) { $ms.Write($item, 0, $item.Length); continue }
        if ($item -is [string]) { $text = $item }
        elseif ($item -is [System.Management.Automation.InformationRecord]) { $text = [string]$item }
        else { $text = ConvertTo-Json -InputObject $item -Depth 64 -Compress }
        $chunk = $utf8.GetBytes((Normalize-Newlines $text) + "` + "`n" + `")
        $ms.Write($chunk, 0, $chunk.Length)
    }
    return ,$ms.ToArray()
}
# Read-CommandStdin pulls the upstream byte pipe through the shell callback the
# command request named, a chunk at a time until EOF, and returns it as the
# lines a filter reads from $input. The bytes never touch the worker's own
# protocol stdin; they ride the same callback frames the Python filter uses.
function Read-CommandStdin($cbid) {
    $buf = New-Object System.IO.MemoryStream
    while ($true) {
        Write-Protocol @{ id = 0; call = 'shell'; callback = $cbid }
        $reply = $stdinReader.ReadLine()
        if ($null -eq $reply) { break }
        $r = ConvertFrom-Json -InputObject $reply -AsHashtable
        if (-not $r['ok']) {
            $msg = 'pipeline stdin read failed'
            if ($r.ContainsKey('error') -and $r['error']) { $msg = [string]$r['error']['message'] }
            throw $msg
        }
        $chunk = Dec $r['result']
        if ($null -eq $chunk -or $chunk.Length -eq 0) { break }
        $buf.Write($chunk, 0, $chunk.Length)
    }
    $text = (New-Object System.Text.UTF8Encoding($false)).GetString($buf.ToArray())
    $lines = New-Object System.Collections.Generic.List[string]
    $sr = New-Object System.IO.StringReader($text)
    while ($true) { $ln = $sr.ReadLine(); if ($null -eq $ln) { break }; [void]$lines.Add($ln) }
    return ,$lines.ToArray()
}
`

const powershellWorkerLoop = `
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
            'command' {
                # The command adapter: argv words are positional strings, the
                # function runs as a filter, and its return is a shell exit
                # status. A missing function is the shell's command-not-found,
                # not a PowerShell failure.
                $fname = [string]$req['name']
                $cmd = Get-Command -Name $fname -CommandType Function -ErrorAction SilentlyContinue
                if ($null -eq $cmd) {
                    $res = @{ id = $rid; ok = $false; error = @{ code = 'CommandNotFound'; message = ('no PowerShell function ' + $fname); help = '' }; stdout = ''; stderr = '' }
                } else {
                    $argv = @()
                    if ($req.ContainsKey('args') -and $null -ne $req['args']) {
                        foreach ($a in @($req['args'])) { $argv += ,[string]$a }
                    }
                    $stdinData = $null
                    if ($req.ContainsKey('stdin') -and $null -ne $req['stdin']) {
                        $stdinData = Read-CommandStdin ([int64]$req['stdin'])
                    }
                    $errFile = New-TemporaryFile
                    $warnFile = New-TemporaryFile
                    $value = $null
                    $failure = $null
                    # A native child's exit code is the status; reset it so a
                    # code left by an earlier command never leaks into this one.
                    $global:LASTEXITCODE = 0
                    try {
                        # Non-terminating errors and warnings (streams 2 and 3)
                        # are captured as stderr; verbose and debug are dropped;
                        # Write-Host (stream 6) merges into the success stream so
                        # its text renders to stdout in order. A terminating
                        # error is caught below.
                        # Invoke the resolved function command, not the bare
                        # name: a name like Echo or Sort resolves to a built-in
                        # alias or cmdlet first, never the island's function.
                        if ($null -ne $stdinData) {
                            $value = $stdinData | & $cmd @argv 2> $errFile.FullName 3> $warnFile.FullName 4> $null 5> $null 6>&1
                        } else {
                            $value = & $cmd @argv 2> $errFile.FullName 3> $warnFile.FullName 4> $null 5> $null 6>&1
                        }
                    } catch {
                        $failure = $_
                    }
                    $e = (Read-CaptureText $errFile.FullName) + (Read-CaptureText $warnFile.FullName)
                    $status = 0
                    $out = [byte[]]@()
                    if ($failure) {
                        # A terminating error ends the command: its message is
                        # the command form of the error that binds to err in a
                        # typed call, and the status is 1.
                        $status = 1
                        $env2 = Envelope-Error $failure
                        $e += [string]$env2['message'] + "` + "`n" + `"
                    } else {
                        $out = Render-CommandOutput $value
                        # A native child's exit code propagates; a clean run is 0.
                        if ($null -ne $global:LASTEXITCODE) { $status = [int]$global:LASTEXITCODE }
                    }
                    # Stdout crosses as base64 bytes so a byte array survives exactly;
                    # stderr is diagnostic text with LF line endings.
                    $res = @{ id = $rid; ok = $true; result = @{ status = $status; stdout = (Enc $out) }; stdout = ''; stderr = (Normalize-Newlines $e) }
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
