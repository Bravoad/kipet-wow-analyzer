#Requires -Version 7.0
[CmdletBinding()]
param(
    [ValidatePattern('^[a-z_][a-z0-9_]{0,62}$')]
    [string]$SourceDatabase = 'kipet',
    [ValidateRange(1, 65535)]
    [int]$PostgresPort = 5432,
    [switch]$KeepArchive
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$workspaceRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$composeFile = Join-Path $workspaceRoot 'compose.yaml'
$runID = [Guid]::NewGuid().ToString('N')
$databaseName = 'kipet_recovery_drill_' + $runID.Substring(0, 16)
$containerArchive = '/tmp/kipet-recovery-' + $runID + '.dump'
$artifactDirectory = [IO.Path]::GetFullPath((Join-Path $workspaceRoot 'artifacts/recovery'))
$archivePath = Join-Path $artifactDirectory ($runID + '.dump')
$reportPath = Join-Path $artifactDirectory ($runID + '.json')
$createdDatabase = $false
$oldDatabaseURL = $env:RECOVERY_DATABASE_URL
$oldToken = $env:RECOVERY_DRILL_TOKEN
$started = [DateTimeOffset]::UtcNow
$timer = [Diagnostics.Stopwatch]::StartNew()
$report = [ordered]@{ run_id = $runID; source = 'fixture'; started_at = $started.ToString('O'); success = $false; archive_kept = [bool]$KeepArchive }

function Invoke-Compose {
    param([Parameter(Mandatory)][string[]]$CommandArguments)
    $output = & docker compose --file $composeFile @CommandArguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Docker Compose command failed (exit $LASTEXITCODE). $($output -join [Environment]::NewLine)"
    }
    return $output
}
function Invoke-SQL {
    param([string]$Database, [string]$SQL)
    return Invoke-Compose -CommandArguments @('exec', '-T', 'postgres', 'psql', '-U', 'kipet', '--dbname', $Database, '--no-psqlrc', '--tuples-only', '--no-align', '--set', 'ON_ERROR_STOP=1', '--command', $SQL)
}
function Assert-DrillDatabase {
    if ($databaseName -notmatch '^kipet_recovery_drill_[a-f0-9]{16}$' -or $databaseName -eq $SourceDatabase) {
        throw 'Refusing a non-drill database target.'
    }
}

Push-Location $workspaceRoot
try {
    # The source is only read. Never adopt an unmarked or live database for this drill.
    $mode = (Invoke-SQL -Database $SourceDatabase -SQL "SELECT value FROM app_settings WHERE key='provider_mode'") -join ''
    $combatMode = (Invoke-SQL -Database $SourceDatabase -SQL "SELECT value FROM app_settings WHERE key='combat_provider_mode'") -join ''
    if ($mode.Trim() -ne 'fixture' -or $combatMode.Trim() -ne 'fixture') { throw 'Recovery drill requires an explicitly marked fixture database for both providers.' }
    Assert-DrillDatabase
    $exists = (Invoke-SQL -Database 'postgres' -SQL "SELECT count(*) FROM pg_database WHERE datname='$databaseName'") -join ''
    if ($exists.Trim() -ne '0') { throw 'Generated drill database already exists; refusing to reuse it.' }
    New-Item -ItemType Directory -Path $artifactDirectory -Force | Out-Null
    $report.migrations = [int]((Invoke-SQL -Database $SourceDatabase -SQL 'SELECT count(*) FROM schema_migrations') -join '')
    Invoke-Compose -CommandArguments @('exec', '-T', 'postgres', 'pg_dump', '-U', 'kipet', '--dbname', $SourceDatabase, '--format=custom', '--no-owner', '--no-privileges', '--file', $containerArchive) | Out-Null
    Invoke-Compose -CommandArguments @('cp', "postgres:$containerArchive", $archivePath) | Out-Null
    $report.backup_ms = $timer.ElapsedMilliseconds
    $report.archive_sha256 = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    $report.archive_bytes = (Get-Item -LiteralPath $archivePath).Length
    Invoke-SQL -Database 'postgres' -SQL "CREATE DATABASE $databaseName" | Out-Null
    $createdDatabase = $true
    $restoreTimer = [Diagnostics.Stopwatch]::StartNew()
    Invoke-Compose -CommandArguments @('exec', '-T', 'postgres', 'pg_restore', '-U', 'kipet', '--dbname', $databaseName, '--exit-on-error', '--no-owner', '--no-privileges', $containerArchive) | Out-Null
    Invoke-SQL -Database $databaseName -SQL "CREATE TABLE recovery_drill_guard(token text PRIMARY KEY); INSERT INTO recovery_drill_guard VALUES('$runID')" | Out-Null
    $restoredMigrations = [int]((Invoke-SQL -Database $databaseName -SQL 'SELECT count(*) FROM schema_migrations') -join '')
    if ($restoredMigrations -ne $report.migrations) { throw 'Restored migration ledger does not match source.' }
    # This helper refuses every database except the fresh random name AND matching guard token.
    # No API or worker is started against the restored copy, and no source credentials are needed.
    $env:RECOVERY_DATABASE_URL = "postgres://kipet:kipet@127.0.0.1:$PostgresPort/$databaseName`?sslmode=disable"
    $env:RECOVERY_DRILL_TOKEN = $runID
    $verification = & go run ./deploy/operations/recoverycheck
    if ($LASTEXITCODE -ne 0) { throw 'Restored data verification failed.' }
    $report.verification = (($verification -join [Environment]::NewLine) | ConvertFrom-Json)
    $report.restore_verify_ms = $restoreTimer.ElapsedMilliseconds
    $report.success = $true
}
finally {
    $env:RECOVERY_DATABASE_URL = $oldDatabaseURL
    $env:RECOVERY_DRILL_TOKEN = $oldToken
    $cleanupFailure = $null
    try {
        if ($createdDatabase) {
            Assert-DrillDatabase
            # No FORCE: an unexpected connection fails closed rather than terminating another process.
            Invoke-SQL -Database 'postgres' -SQL "DROP DATABASE $databaseName" | Out-Null
            $remaining = (Invoke-SQL -Database 'postgres' -SQL "SELECT count(*) FROM pg_database WHERE datname='$databaseName'") -join ''
            if ($remaining.Trim() -ne '0') { throw 'Drill database cleanup was not confirmed.' }
        }
        $report.database_removed = $true
    }
    catch { $cleanupFailure = $_; $report.database_removed = $false }
    try {
        if ($containerArchive -notmatch '^/tmp/kipet-recovery-[a-f0-9]{32}\.dump$') { throw 'Unexpected container archive path.' }
        Invoke-Compose -CommandArguments @('exec', '-T', 'postgres', 'rm', '-f', '--', $containerArchive) | Out-Null
        if (!$KeepArchive -and (Test-Path -LiteralPath $archivePath)) {
            $resolvedArchive = [IO.Path]::GetFullPath($archivePath)
            if ($resolvedArchive -ne (Join-Path $artifactDirectory ($runID + '.dump'))) { throw 'Unexpected local archive path.' }
            Remove-Item -LiteralPath $resolvedArchive
        }
    }
    catch { if (!$cleanupFailure) { $cleanupFailure = $_ } }
    $report.completed_at = [DateTimeOffset]::UtcNow.ToString('O')
    $report.total_ms = $timer.ElapsedMilliseconds
    $report.success = $report.success -and !$cleanupFailure
    if (Test-Path -LiteralPath $artifactDirectory) { $report | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $reportPath -Encoding utf8 }
    Pop-Location
    if ($cleanupFailure) { throw $cleanupFailure }
}
Write-Host "PASS: fixture backup restored, publication guards checked, drill database removed. Report: $reportPath"
