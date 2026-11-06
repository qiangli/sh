param(
 [Parameter(Mandatory=$true)][string]$SourceDir,
 [Parameter(Mandatory=$true)][string]$GoExe,
 [Parameter(Mandatory=$true)][string]$ProviderDir,
 [Parameter(Mandatory=$true)][string]$Scratch,
 [Parameter(Mandatory=$true)][string]$ExpectedCommit,
 [Parameter(Mandatory=$true)][string]$GoSHA256,
 [Parameter(Mandatory=$true)][string]$PwshSHA256,
 [string]$CacheSeed,
 [string]$OfficialFixtures,
 [switch]$ReviewFollowup
)
$ErrorActionPreference = 'Stop'
$source = [IO.Path]::GetFullPath($SourceDir)
$go = [IO.Path]::GetFullPath($GoExe)
$provider = [IO.Path]::GetFullPath($ProviderDir)
$work = [IO.Path]::GetFullPath($Scratch)
if ((Get-Content -Raw (Join-Path $source '.s562-source-commit')).Trim() -cne $ExpectedCommit) { throw 'Wrong source commit' }
if ((Get-FileHash -Algorithm SHA256 $go).Hash -ine $GoSHA256) { throw 'Wrong Go provider' }
if ((Get-FileHash -Algorithm SHA256 (Join-Path $provider 'pwsh.exe')).Hash -ine $PwshSHA256) { throw 'Wrong PowerShell provider' }
foreach ($line in Get-Content (Join-Path $source '.s562-source-sha256')) {
 $digest,$path = $line -split '  ',2
 if ((Get-FileHash -Algorithm SHA256 (Join-Path $source $path)).Hash -ine $digest) { throw ('Source mismatch: '+$path) }
}
if (Test-Path $work) { throw 'Gate requires fresh scratch directory' }
New-Item -ItemType Directory $work | Out-Null
$env:HOME = Join-Path $work 'home'
$env:USERPROFILE = $env:HOME
$env:LOCALAPPDATA = Join-Path $work 'localappdata'
$env:APPDATA = Join-Path $work 'appdata'
$env:GOPATH = Join-Path $work 'gopath'
$env:GOMODCACHE = Join-Path $work 'gomod'
$env:GOCACHE = Join-Path $work 'gocache'
$env:TEMP = Join-Path $work 'tmp'; $env:TMP = $env:TEMP
$env:PSModuleAnalysisCachePath = Join-Path $work 'ps-analysis'
$env:PATH = $provider + ';' + (Split-Path $go) + ';' + $env:SystemRoot + '\System32'
$env:BASHPP_PWSH = Join-Path $provider 'pwsh.exe'
$env:GOPROXY='off'; $env:GOSUMDB='off'; $env:GOENV='off'
$env:GOWORK='off'; $env:GOTOOLCHAIN='local'
$env:POWERSHELL_TELEMETRY_OPTOUT='1'; $env:POWERSHELL_UPDATECHECK='Off'
foreach($d in @($env:HOME,$env:LOCALAPPDATA,$env:APPDATA,$env:GOPATH,$env:GOMODCACHE,$env:GOCACHE,$env:TEMP)) { New-Item -ItemType Directory $d | Out-Null }
if ($CacheSeed) {
 foreach ($pair in @(@('gomod',$env:GOMODCACHE),@('go-build',$env:GOCACHE))) {
  $seed = Join-Path $CacheSeed $pair[0]
  if (!(Test-Path $seed)) { throw ('Missing private cache seed: '+$seed) }
  Copy-Item (Join-Path $seed '*') $pair[1] -Recurse -Force
 }
}
Get-FileHash -Algorithm SHA256 $go,$env:BASHPP_PWSH | ConvertTo-Json | Set-Content (Join-Path $work 'provider-hashes.json')
Set-Location $source
$steps = @(
 @{package='polyglot'; names=@('TestCSharpGraphLoadContext','TestCSharpGraphCanceledLoad','TestCSharpGraphValidation','TestCSharpGraphSnapshot','TestCSharpGraphRuntime','TestCSharpGraphContentIdentity','TestCSharpGraphOverlay','TestCSharpGraphExternAlias','TestCSharpAnalyzeExportsByReflection','TestCSharpTypedCallsErrorsAndState','TestPowerShellPersistentCallsTypedValuesAndOutput')},
 @{package='lower'; names=@('TestCSharpAssembliesBothModesWithoutSourceAssets','TestCSharpFenceInterpretedNativeParity','TestPowerShellFenceInterpretedNativeParity')}
)
if ($ReviewFollowup) { $steps[0].names = @('TestCSharpGraphRuntime') }
if ($OfficialFixtures) {
 $env:BASHPP_CSHARP_OFFICIAL_GRAPHS = [IO.Path]::GetFullPath($OfficialFixtures)
 $steps[1].names += 'TestCSharpOfficialGraphsBothModes'
}
& $go list -mod=readonly -tags full -deps -test ./polyglot ./lower 1> (Join-Path $work 'dependency-list.stdout') 2> (Join-Path $work 'dependency-list.stderr')
if ($LASTEXITCODE -ne 0) { throw 'Offline dependency closure failed' }
foreach($step in $steps) {
 $log = Join-Path $work ($step.package+'.jsonl')
 $errlog = Join-Path $work ($step.package+'.stderr')
 $pattern = '^(' + ($step.names -join '|') + ')$'
 $argv = @('test','-json','-tags','full','-timeout=20m',('./'+$step.package),'-run',$pattern,'-count=1')
 $argv | ConvertTo-Json | Set-Content (Join-Path $work ($step.package+'.argv.json'))
 & $go @argv 1> $log 2> $errlog
 $code=$LASTEXITCODE
 $code | Set-Content (Join-Path $work ($step.package+'.status'))
 if ($code -ne 0) { throw ('Gate failed: '+$step.package+' exit '+$code) }
 $events = @(Get-Content $log | ForEach-Object { $_ | ConvertFrom-Json })
 if (@($events | Where-Object { $_.Action -eq 'skip' -or $_.Action -eq 'fail' }).Count -ne 0) { throw 'Skipped or failed test event' }
 foreach($name in $step.names) {
   if (@($events | Where-Object { $_.Action -eq 'pass' -and $_.Test -ceq $name }).Count -ne 1) { throw ('Missing named PASS: '+$name) }
 }
 if (@($events | Where-Object { $_.Action -eq 'pass' -and !$_.Test }).Count -ne 1) { throw 'Missing package PASS' }
 Write-Output ('PASS '+$step.package+' named tests='+$step.names.Count)
}
