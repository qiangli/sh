param(
    [Parameter(Mandatory=$true)][string]$Fixture,
    [Parameter(Mandatory=$true)][ValidateSet('file-L18','using-directive-L133')][string]$Case
)
$ErrorActionPreference = 'Stop'
$text = [System.IO.File]::ReadAllText($Fixture)
$match = [regex]::Match($text, '(?s)~~~csharp as cs\r?\n(.*?)\r?\n~~~')
if (-not $match.Success) { throw "missing exact C# fence in $Fixture" }
$source = [regex]::Replace($match.Groups[1].Value, '(?m)^public static int Run\(\).*(?:\r?\n|$)', '')
if ($source -eq $match.Groups[1].Value) { throw 'missing corpus Run harness method' }
$assembly = Join-Path ([System.IO.Path]::GetTempPath()) ('csharp-unit-native-' + [guid]::NewGuid().ToString('N') + '.dll')
try {
    Add-Type -TypeDefinition $source -Language CSharp -OutputAssembly $assembly -OutputType Library -IgnoreWarnings -WarningAction SilentlyContinue -ErrorAction Stop
    $loaded = [System.Reflection.Assembly]::LoadFrom($assembly)
    if ($Case -eq 'file-L18') {
        $type = $loaded.GetType('__Entry', $true)
        $method = $type.GetMethod('Main', [System.Reflection.BindingFlags]'Public,Static')
        if ($null -eq $method) { throw 'native __Entry.Main missing' }
    } else {
        $type = $loaded.GetType('Program', $true)
        $method = $type.GetMethod('Main', [System.Reflection.BindingFlags]'Public,Static')
        if ($null -eq $method) { throw 'native Program.Main missing' }
    }
    [void]$method.Invoke($null, @())
    Write-Output "native $Case PASS"
} finally {
    Remove-Item -LiteralPath $assembly -ErrorAction SilentlyContinue
}
