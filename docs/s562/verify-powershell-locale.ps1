param(
    [Parameter(Mandatory=$true)][string]$Go,
    [Parameter(Mandatory=$true)][string]$Pwsh,
    [Parameter(Mandatory=$true)][string]$Evidence,
    [Parameter(Mandatory=$true)][string]$ExpectedCommit
)
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path "$PSScriptRoot/../..").Path
if (Test-Path $Evidence) { throw 'Evidence directory must be new' }
New-Item -ItemType Directory $Evidence | Out-Null
$Evidence = (Resolve-Path $Evidence).Path
$head = (Get-Content "$root/.s562-source-commit" -Raw).Trim()
if ($head -ne $ExpectedCommit) { throw 'Wrong source commit' }
Get-Content "$root/.s562-source-sha256" | ForEach-Object {
    $digest,$name = $_ -split '  ',2
    if ((Get-FileHash "$root/$name" -Algorithm SHA256).Hash.ToLower() -ne $digest) { throw "Source hash mismatch: $name" }
}
@{commit=$head; go=$Go; pwsh=$Pwsh; go_sha256=(Get-FileHash $Go).Hash; pwsh_sha256=(Get-FileHash $Pwsh).Hash; platform=[Environment]::OSVersion.VersionString} | ConvertTo-Json | Set-Content "$Evidence/identity.json"
$env:BASHPP_PWSH=$Pwsh
$env:PATH="$(Split-Path $Pwsh);$env:PATH"
$env:GOWORK='off'; $env:GOTOOLCHAIN='local'
Push-Location $root
try {
    $groups = @{
        polyglot=@('TestPowerShellExplicitLocaleEnvironment','TestPowerShellExecutionEnvironment','TestPowerShellExplicitInvariantOverridesProvisioner','TestDiscoverPowerShellEnvironmentCarriesProvisionedRuntimeControls')
        lower=@('TestPowerShellLocaleNativeInterpretedCompiled','TestCSharpLocaleNativeInterpretedCompiled','TestCSharpLocaleInterpolationRecipe')
    }
    foreach ($package in @('polyglot','lower')) {
        $names=$groups[$package]
        & $Go test -json -tags full -count=1 -timeout=5m "./$package" -run "^($($names -join '|'))$" 1> "$Evidence/$package.jsonl" 2> "$Evidence/$package.stderr"
        $status=$LASTEXITCODE
        $status | Set-Content "$Evidence/$package.status"
        if ($status -ne 0) { throw "$package test process failed" }
        $events=@(Get-Content "$Evidence/$package.jsonl" | ForEach-Object { $_ | ConvertFrom-Json })
        if (@($events | Where-Object { $_.Action -in @('skip','fail') }).Count) { throw "$package skip or failure" }
        $passes=@($events | Where-Object { $_.Action -eq 'pass' })
        foreach ($name in $names) { if ($name -notin $passes.Test) { throw "Missing PASS $name" } }
        if (-not @($passes | Where-Object { -not $_.Test }).Count) { throw 'Missing package PASS' }
        if ($package -eq 'lower') {
            foreach ($locale in @('en_US.UTF-8','de_DE.UTF-8')) {
                foreach ($test in @('TestPowerShellLocaleNativeInterpretedCompiled','TestCSharpLocaleNativeInterpretedCompiled')) {
                    if ("$test/$locale" -notin $passes.Test) { throw "Missing locale $test/$locale" }
                }
            }
        }
        Write-Output "PASS native Windows $package, no skips/failures"
    }
    'PASS relevant native Windows regression; corpus acceptance separate' | Set-Content "$Evidence/PASS"
} finally { Pop-Location }
