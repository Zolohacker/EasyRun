[CmdletBinding()]
param([string]$ModelDirectory = (Join-Path $PSScriptRoot '..\models'))
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath $ModelDirectory).Path
$manifest = [IO.File]::ReadAllLines((Join-Path $root 'SHA256SUMS'))
foreach ($relative in @('Megatron','windows-amd64/libInspireFace.dll')) {
    $entry = $manifest | Where-Object { $_.EndsWith('  '+$relative) } | Select-Object -First 1
    if (-not $entry) { throw "Missing checksum for $relative" }
    $path = Join-Path $root $relative
    $actual = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $entry.Substring(0,64)) { throw "Model/runtime checksum mismatch: $relative" }
}
Write-Host 'Bundled model and Windows runtime checksums verified.'
Write-Host 'Start EasyRun normally: model paths are discovered automatically.'
Write-Host 'Windows native SDK requires Microsoft Visual C++ 2022 x64 runtime.'
