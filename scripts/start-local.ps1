[CmdletBinding()]
param(
    [switch]$Rebuild
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$EnvFile = Join-Path $ProjectRoot ".env.local"
$Executable = Join-Path $ProjectRoot "bin\easyrun.exe"

if (-not (Test-Path -LiteralPath $EnvFile)) {
    & (Join-Path $PSScriptRoot "local-db.ps1") init
}
else {
    & (Join-Path $PSScriptRoot "local-db.ps1") start
}
if ($LASTEXITCODE -ne 0) {
    throw "Local database startup failed."
}

foreach ($line in [System.IO.File]::ReadAllLines($EnvFile)) {
    $trimmed = $line.Trim()
    if (-not $trimmed -or $trimmed.StartsWith("#") -or -not $trimmed.Contains("=")) {
        continue
    }
    $separator = $trimmed.IndexOf("=")
    $name = $trimmed.Substring(0, $separator).Trim()
    $value = $trimmed.Substring($separator + 1)
    [Environment]::SetEnvironmentVariable($name, $value, "Process")
}

if ($Rebuild -or -not (Test-Path -LiteralPath $Executable)) {
    New-Item -ItemType Directory -Path (Split-Path $Executable -Parent) -Force | Out-Null
    & go build -buildvcs=false -o $Executable .\cmd\server
    if ($LASTEXITCODE -ne 0) {
        throw "Application build failed."
    }
}

Write-Host "Starting local application at http://127.0.0.1:8080"
Write-Host "Press Ctrl+C to stop the application; the local database keeps running."
& $Executable
exit $LASTEXITCODE
