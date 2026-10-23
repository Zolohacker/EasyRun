[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet("init", "start", "stop", "status", "backup")]
    [string]$Command = "status",

    [ValidateRange(1024, 65535)]
    [int]$Port = 56432
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$LocalRoot = Join-Path $ProjectRoot ".local"
$DatabaseRoot = Join-Path $LocalRoot "postgres"
$DataDir = Join-Path $DatabaseRoot "data"
$LogFile = Join-Path $DatabaseRoot "postgres.log"
$AdminEnvFile = Join-Path $DatabaseRoot "admin.env"
$AppEnvFile = Join-Path $ProjectRoot ".env.local"
$BackupDir = Join-Path $DatabaseRoot "backups"

$AdminUser = "running_admin"
$AppUser = "running_app"
$DatabaseName = "easyrun"
$HostAddress = "127.0.0.1"

function Write-Utf8NoBom {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Value
    )

    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $Value, $encoding)
}

function New-RandomHex {
    param([int]$ByteCount = 24)

    $bytes = New-Object byte[] $ByteCount
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
    }
    finally {
        $generator.Dispose()
    }
    return -join ($bytes | ForEach-Object { $_.ToString("x2") })
}

function New-RandomBase64 {
    param([int]$ByteCount = 32)

    $bytes = New-Object byte[] $ByteCount
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
    }
    finally {
        $generator.Dispose()
    }
    return [Convert]::ToBase64String($bytes)
}

function Resolve-DatabaseTool {
    param([Parameter(Mandatory = $true)][string]$Name)

    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($null -ne $command) {
        return $command.Source
    }

    $searchRoots = @()
    if ($env:ProgramFiles) {
        $searchRoots += (Join-Path $env:ProgramFiles "PostgreSQL")
    }
    if (${env:ProgramFiles(x86)}) {
        $searchRoots += (Join-Path ${env:ProgramFiles(x86)} "PostgreSQL")
    }

    foreach ($root in $searchRoots) {
        if (-not (Test-Path -LiteralPath $root)) {
            continue
        }
        $match = Get-ChildItem -Path (Join-Path $root "*\bin\$Name") -File -ErrorAction SilentlyContinue |
            Sort-Object FullName -Descending |
            Select-Object -First 1
        if ($null -ne $match) {
            return $match.FullName
        }
    }

    throw "Required database tool '$Name' was not found."
}

function Get-EnvValue {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Name
    )

    if (-not (Test-Path -LiteralPath $Path)) {
        return $null
    }
    foreach ($line in [System.IO.File]::ReadAllLines($Path)) {
        if ($line -match ('^\s*' + [regex]::Escape($Name) + '=(.*)$')) {
            return $Matches[1].Trim()
        }
    }
    return $null
}

function Set-ManagedDatabaseConfig {
    $configPath = Join-Path $DataDir "postgresql.conf"
    $content = [System.IO.File]::ReadAllText($configPath)
    $pattern = '(?s)\r?\n?# BEGIN (?:running-platform|EasyRun) local settings.*?# END (?:running-platform|EasyRun) local settings\r?\n?'
    $content = [regex]::Replace($content, $pattern, "")
    $newline = [Environment]::NewLine
    $block = @(
        "# BEGIN EasyRun local settings"
        "listen_addresses = '$HostAddress'"
        "port = $Port"
        "password_encryption = 'scram-sha-256'"
        "max_connections = 40"
        "shared_buffers = '128MB'"
        "timezone = 'Asia/Shanghai'"
        "log_timezone = 'Asia/Shanghai'"
        "logging_collector = on"
        "log_directory = 'log'"
        "log_filename = 'database-%Y-%m-%d.log'"
        "log_rotation_age = 1440"
        "log_truncate_on_rotation = on"
        "log_min_duration_statement = 500"
        "# END EasyRun local settings"
    ) -join $newline
    Write-Utf8NoBom -Path $configPath -Value ($content.TrimEnd() + $newline + $newline + $block + $newline)
}

function Test-DatabaseRunning {
    if (-not (Test-Path -LiteralPath (Join-Path $DataDir "PG_VERSION"))) {
        return $false
    }
    $pgCtl = Resolve-DatabaseTool "pg_ctl.exe"
    & $pgCtl status -D $DataDir *> $null
    return ($LASTEXITCODE -eq 0)
}

function Start-Database {
    if (-not (Test-Path -LiteralPath (Join-Path $DataDir "PG_VERSION"))) {
        throw "The local database is not initialized. Run: .\scripts\local-db.ps1 init"
    }
    if (Test-DatabaseRunning) {
        Write-Host "Local database is already running on ${HostAddress}:$Port."
        return
    }

    $pgCtl = Resolve-DatabaseTool "pg_ctl.exe"
    New-Item -ItemType Directory -Path $DatabaseRoot -Force | Out-Null
    & $pgCtl start -D $DataDir -l $LogFile -w -t 60
    if ($LASTEXITCODE -ne 0) {
        throw "Local database startup failed. See $LogFile"
    }
    Write-Host "Local database started on ${HostAddress}:$Port."
}

function Stop-Database {
    if (-not (Test-Path -LiteralPath (Join-Path $DataDir "PG_VERSION"))) {
        Write-Host "Local database has not been initialized."
        return
    }
    if (-not (Test-DatabaseRunning)) {
        Write-Host "Local database is already stopped."
        return
    }

    $pgCtl = Resolve-DatabaseTool "pg_ctl.exe"
    & $pgCtl stop -D $DataDir -m fast -w -t 60
    if ($LASTEXITCODE -ne 0) {
        throw "Local database shutdown failed."
    }
    Write-Host "Local database stopped."
}

function Write-AppEnvironment {
    param([Parameter(Mandatory = $true)][string]$AppPassword)

    $values = [ordered]@{
        HTTP_ADDR                   = "127.0.0.1:8080"
        EASYRUN_DATA_DIR            = "data"
        DATABASE_URL                = "postgres://${AppUser}:${AppPassword}@${HostAddress}:${Port}/${DatabaseName}?sslmode=disable"
        DATABASE_MAX_CONNS          = "12"
        DATABASE_MIN_CONNS          = "2"
        DATABASE_CONNECT_TIMEOUT    = "5s"
        MAP_API_BASE_URL            = "https://restapi.amap.com"
        MAP_API_PATH                = "/v3/direction/walking"
        MAP_API_TIMEOUT             = "8s"
        MAP_API_QPS                 = "3"
        MAP_API_BURST               = "2"
        MAP_API_MAX_RETRIES         = "2"
        MAP_API_CACHE_TTL           = "24h"
        MAP_API_CACHE_SIZE          = "5000"
        MAP_ROUTE_CANDIDATES        = "4"
        AUTOMATION_WORKERS          = "2"
        AUTOMATION_POLL_INTERVAL    = "10s"
        AUTOMATION_RUN_TIMEOUT      = "90m"
        PROTOCOL_BASE_URL           = "https://api.huachenjie.com/run-front"
        PROTOCOL_WEB_BASE_URL       = "https://mh5.huachenjie.com"
        PROTOCOL_ENVIRONMENT        = "9"
        PROTOCOL_TIMEOUT            = "20s"
    }

    if (Test-Path -LiteralPath $AppEnvFile) {
        foreach ($line in [System.IO.File]::ReadAllLines($AppEnvFile)) {
            $trimmed = $line.Trim()
            if (-not $trimmed -or $trimmed.StartsWith("#") -or -not $trimmed.Contains("=")) {
                continue
            }
            $separator = $trimmed.IndexOf("=")
            $key = $trimmed.Substring(0, $separator).Trim()
            $value = $trimmed.Substring($separator + 1).Trim()
            if ($key -and $key -notin @("DATABASE_URL", "MAP_API_KEY")) {
                $values[$key] = $value
            }
        }
    }

    # Migrate the incorrect Sd environment pair written by earlier platform builds.
    # The observed 8.7.9 startup path calls k43.a(9), whose production base is api.huachenjie.com.
    if ($values["PROTOCOL_BASE_URL"] -eq "https://sd-api.huachenjie.com/run-front" -and
        $values["PROTOCOL_ENVIRONMENT"] -eq "11") {
        $values["PROTOCOL_BASE_URL"] = "https://api.huachenjie.com/run-front"
        $values["PROTOCOL_ENVIRONMENT"] = "9"
    }

    $lines = @("# Generated by scripts/local-db.ps1; kept outside version control.")
    foreach ($entry in $values.GetEnumerator()) {
        $lines += "$($entry.Key)=$($entry.Value)"
    }
    Write-Utf8NoBom -Path $AppEnvFile -Value (($lines -join [Environment]::NewLine) + [Environment]::NewLine)
}

function Initialize-Database {
    New-Item -ItemType Directory -Path $DatabaseRoot -Force | Out-Null

    $adminPassword = Get-EnvValue -Path $AdminEnvFile -Name "PGPASSWORD"
    $appPassword = Get-EnvValue -Path $AdminEnvFile -Name "APP_PASSWORD"
    $isNewCluster = -not (Test-Path -LiteralPath (Join-Path $DataDir "PG_VERSION"))

    if ($isNewCluster) {
        if (-not $adminPassword) { $adminPassword = New-RandomHex }
        if (-not $appPassword) { $appPassword = New-RandomHex }

        $passwordFile = Join-Path $DatabaseRoot ".init-password"
        Write-Utf8NoBom -Path $passwordFile -Value $adminPassword
        try {
            $initdb = Resolve-DatabaseTool "initdb.exe"
            & $initdb -D $DataDir -U $AdminUser --encoding=UTF8 --locale=C --auth-local=scram-sha-256 --auth-host=scram-sha-256 --data-checksums "--pwfile=$passwordFile"
            if ($LASTEXITCODE -ne 0) {
                throw "Database cluster initialization failed."
            }
        }
        finally {
            if (Test-Path -LiteralPath $passwordFile) {
                Remove-Item -LiteralPath $passwordFile -Force
            }
        }

        $adminLines = @(
            "# Local maintenance credentials; this file is ignored by version control."
            "PGHOST=$HostAddress"
            "PGPORT=$Port"
            "PGUSER=$AdminUser"
            "PGPASSWORD=$adminPassword"
            "PGDATABASE=postgres"
            "APP_USER=$AppUser"
            "APP_PASSWORD=$appPassword"
            "APP_DATABASE=$DatabaseName"
        )
        Write-Utf8NoBom -Path $AdminEnvFile -Value (($adminLines -join [Environment]::NewLine) + [Environment]::NewLine)
    }
    elseif (-not $adminPassword -or -not $appPassword) {
        throw "Local credential file is missing or incomplete: $AdminEnvFile"
    }

    Set-ManagedDatabaseConfig
    Start-Database

    $psql = Resolve-DatabaseTool "psql.exe"
    $createdb = Resolve-DatabaseTool "createdb.exe"
    $oldPassword = $env:PGPASSWORD
    try {
        $env:PGPASSWORD = $adminPassword
        $common = @("-X", "-h", $HostAddress, "-p", "$Port", "-U", $AdminUser, "-d", "postgres", "-v", "ON_ERROR_STOP=1")
        $roleExistsOutput = & $psql @common -qAt -c "SELECT 1 FROM pg_roles WHERE rolname = '$AppUser';"
        if ($LASTEXITCODE -ne 0) { throw "Role lookup failed." }
        $roleExists = (($roleExistsOutput | Out-String).Trim() -eq "1")
        if ($roleExists) {
            & $psql @common -q -c "ALTER ROLE $AppUser WITH LOGIN PASSWORD '$appPassword' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;"
        }
        else {
            & $psql @common -q -c "CREATE ROLE $AppUser LOGIN PASSWORD '$appPassword' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;"
        }
        if ($LASTEXITCODE -ne 0) { throw "Application role setup failed." }

        $databaseExistsOutput = & $psql @common -qAt -c "SELECT 1 FROM pg_database WHERE datname = '$DatabaseName';"
        if ($LASTEXITCODE -ne 0) { throw "Database lookup failed." }
        $databaseExists = (($databaseExistsOutput | Out-String).Trim() -eq "1")
        if (-not $databaseExists) {
            & $createdb -h $HostAddress -p $Port -U $AdminUser -O $AppUser -E UTF8 $DatabaseName
            if ($LASTEXITCODE -ne 0) { throw "Application database creation failed." }
        }
        & $psql @common -q -c "ALTER DATABASE $DatabaseName OWNER TO $AppUser; REVOKE ALL ON DATABASE $DatabaseName FROM PUBLIC; GRANT CONNECT, TEMPORARY ON DATABASE $DatabaseName TO $AppUser;"
        if ($LASTEXITCODE -ne 0) { throw "Database ownership setup failed." }
        & $psql -X -h $HostAddress -p $Port -U $AdminUser -d $DatabaseName -v ON_ERROR_STOP=1 -q -c "ALTER SCHEMA public OWNER TO $AppUser; GRANT USAGE, CREATE ON SCHEMA public TO $AppUser;"
        if ($LASTEXITCODE -ne 0) { throw "Schema ownership setup failed." }
    }
    finally {
        $env:PGPASSWORD = $oldPassword
    }

    Write-AppEnvironment -AppPassword $appPassword
    Write-Host "Local database initialized."
    Write-Host "Database: $DatabaseName"
    Write-Host "Application role: $AppUser"
    Write-Host "Environment: $AppEnvFile"
}

function Show-DatabaseStatus {
    if (-not (Test-Path -LiteralPath (Join-Path $DataDir "PG_VERSION"))) {
        Write-Host "Local database has not been initialized."
        return
    }
    if (Test-DatabaseRunning) {
        $psql = Resolve-DatabaseTool "psql.exe"
        $adminPassword = Get-EnvValue -Path $AdminEnvFile -Name "PGPASSWORD"
        $oldPassword = $env:PGPASSWORD
        try {
            $env:PGPASSWORD = $adminPassword
            $details = & $psql -X -h $HostAddress -p $Port -U $AdminUser -d postgres -qAt -c "SELECT current_setting('listen_addresses') || '|' || current_setting('port') || '|' || version();"
            if ($LASTEXITCODE -ne 0) { throw "Database status query failed." }
            Write-Host "running|$details"
        }
        finally {
            $env:PGPASSWORD = $oldPassword
        }
    }
    else {
        Write-Host "stopped|${HostAddress}:$Port"
    }
}

function Backup-Database {
    Start-Database
    $pgDump = Resolve-DatabaseTool "pg_dump.exe"
    $appPassword = Get-EnvValue -Path $AdminEnvFile -Name "APP_PASSWORD"
    if (-not $appPassword) {
        throw "Application database password is missing."
    }
    New-Item -ItemType Directory -Path $BackupDir -Force | Out-Null
    $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $destination = Join-Path $BackupDir "${DatabaseName}-${stamp}.dump"
    $oldPassword = $env:PGPASSWORD
    try {
        $env:PGPASSWORD = $appPassword
        & $pgDump -h $HostAddress -p $Port -U $AppUser -d $DatabaseName -Fc -f $destination
        if ($LASTEXITCODE -ne 0) { throw "Database backup failed." }
    }
    finally {
        $env:PGPASSWORD = $oldPassword
    }
    Write-Output $destination
}

switch ($Command) {
    "init"   { Initialize-Database }
    "start"  { Start-Database }
    "stop"   { Stop-Database }
    "status" { Show-DatabaseStatus }
    "backup" { Backup-Database }
}
