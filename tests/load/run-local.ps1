[CmdletBinding()]
param(
    [ValidateRange(1, 8)][int]$VUs = 8,
    [ValidateRange(1, 20)][int]$DurationSeconds = 20
)
$ErrorActionPreference = 'Stop'
$workspacePath = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$scriptPath = Join-Path $workspacePath 'tests/load'
$resultsPath = Join-Path $workspacePath 'artifacts'
New-Item -ItemType Directory -Force -Path $resultsPath | Out-Null
# The image is pinned to the digest actually used in the local validation.
$k6Image = 'grafana/k6:1.6.1@sha256:a5ad6bc089a08d77c3ec49f3db8c6fa7a148e4073efcac44c675dbaf3568d8e1'
& docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges `
    --mount "type=bind,source=$scriptPath,target=/scripts,readonly" `
    --mount "type=bind,source=$resultsPath,target=/results" `
    -e BASE_URL=http://host.docker.internal:5173 `
    -e "VUS=$VUs" -e "DURATION_SECONDS=$DurationSeconds" `
    -e SUMMARY_PATH=/results/load-fixture-summary.json `
    $k6Image run --no-usage-report /scripts/fixture-read.js
if ($LASTEXITCODE -ne 0) { throw "k6 exited with code $LASTEXITCODE" }
