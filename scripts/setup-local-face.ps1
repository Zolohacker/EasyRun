[CmdletBinding()]
param(
    [string]$ApkPath,
    [ValidateSet("Megatron", "Pikachu")]
    [string]$ModelPack = "Megatron",
    [string]$RuntimeDll
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
if (-not $ApkPath) {
    $ApkPath = Join-Path (Split-Path $ProjectRoot -Parent) "input\SAMPLE.apk"
}
$ApkPath = (Resolve-Path -LiteralPath $ApkPath).Path
$Destination = Join-Path $ProjectRoot "bin\face"
New-Item -ItemType Directory -Path $Destination -Force | Out-Null

Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = [System.IO.Compression.ZipFile]::OpenRead($ApkPath)
try {
    $entryName = "assets/inspireface/$ModelPack"
    $entry = $archive.GetEntry($entryName)
    if ($null -eq $entry) {
        throw "APK entry not found: $entryName"
    }
    $modelPath = Join-Path $Destination $ModelPack
    $input = $entry.Open()
    try {
        $output = [System.IO.File]::Create($modelPath)
        try { $input.CopyTo($output) } finally { $output.Dispose() }
    } finally {
        $input.Dispose()
    }
} finally {
    $archive.Dispose()
}

$dllPath = Join-Path $Destination "libInspireFace.dll"
if ($RuntimeDll) {
    $resolvedDll = (Resolve-Path -LiteralPath $RuntimeDll).Path
    Copy-Item -LiteralPath $resolvedDll -Destination $dllPath -Force
}
if (-not (Test-Path -LiteralPath $dllPath)) {
    throw "A Windows x64 runtime DLL is required. Pass -RuntimeDll <path>."
}

Write-Host "Local face runtime ready:"
Write-Host "  FACE_ENGINE_LIBRARY_PATH=$dllPath"
Write-Host "  FACE_MODEL_PACK_PATH=$(Join-Path $Destination $ModelPack)"
Write-Host "  FACE_DETECT_PIXEL_SIZE=320"
