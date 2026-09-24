param(
    [string]$EnvFile = (Join-Path $PSScriptRoot '..\..\.env'),
    [int]$VitePort = 9245,
    [switch]$ValidateOnly
)

$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$runtimeStage = Join-Path $repoRoot 'build\windows\runtime-stage'
$runtimeManifest = Join-Path $runtimeStage 'runtime-manifest.json'
$sourceSkill = Join-Path $repoRoot 'skills\document'

function Import-DotEnv([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        Write-Host "No local env file at $Path; using process environment."
        return
    }

    $loaded = 0
    foreach ($line in Get-Content -LiteralPath $Path) {
        $entry = $line.Trim()
        if ([string]::IsNullOrWhiteSpace($entry) -or $entry.StartsWith('#')) {
            continue
        }
        if ($entry.StartsWith('export ')) {
            $entry = $entry.Substring(7).Trim()
        }
        $separator = $entry.IndexOf('=')
        if ($separator -le 0) {
            Write-Warning "Ignoring invalid .env line: $line"
            continue
        }

        $key = $entry.Substring(0, $separator).Trim()
        $value = $entry.Substring($separator + 1).Trim()
        if ($key -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
            Write-Warning "Ignoring invalid .env key: $key"
            continue
        }
        if ($value.Length -ge 2) {
            $quotedWithDouble = $value.StartsWith('"') -and $value.EndsWith('"')
            $quotedWithSingle = $value.StartsWith("'") -and $value.EndsWith("'")
            if ($quotedWithDouble -or $quotedWithSingle) {
                $value = $value.Substring(1, $value.Length - 2)
            }
        }

        # Process environment wins so one-off overrides stay predictable.
        if (-not (Test-Path -LiteralPath "Env:$key")) {
            Set-Item -LiteralPath "Env:$key" -Value $value
            $loaded++
        }
    }
    Write-Host "Loaded $loaded environment variable(s) from $Path"
}

function Test-StageSkillStale {
    $stageSkill = Join-Path $runtimeStage 'skills\document'
    if (-not (Test-Path -LiteralPath (Join-Path $stageSkill 'skill.json') -PathType Leaf)) {
        return $true
    }
    $sourceLatest = Get-ChildItem -LiteralPath $sourceSkill -Recurse -File |
        Sort-Object LastWriteTimeUtc -Descending |
        Select-Object -First 1
    $stageLatest = Get-ChildItem -LiteralPath $stageSkill -Recurse -File |
        Sort-Object LastWriteTimeUtc -Descending |
        Select-Object -First 1
    if ($null -eq $sourceLatest -or $null -eq $stageLatest) {
        return $true
    }
    return $sourceLatest.LastWriteTimeUtc -gt $stageLatest.LastWriteTimeUtc
}

Import-DotEnv $EnvFile

if ([string]::IsNullOrWhiteSpace($env:HITK_SKILL_COMPAT)) {
    $env:HITK_SKILL_COMPAT = 'full'
}
$validModes = @('off', 'prompt', 'resolve', 'full')
if ($env:HITK_SKILL_COMPAT -notin $validModes) {
    throw "HITK_SKILL_COMPAT must be one of: $($validModes -join ', ')"
}

if (-not (Test-Path -LiteralPath $runtimeManifest -PathType Leaf) -or (Test-StageSkillStale)) {
    Write-Host 'Staging bundled Skill runtimes...'
    & (Join-Path $PSScriptRoot 'fetch-runtime.ps1') -Action Stage
    if ($LASTEXITCODE -ne 0) {
        throw 'Failed to stage the bundled Skill runtimes'
    }
}

$requiredRuntimeFiles = @(
    'python\python.exe',
    'node\node.exe',
    'uv\uv.exe',
    'pnpm\bin\pnpm.cjs'
)
foreach ($relativePath in $requiredRuntimeFiles) {
    $candidate = Join-Path $runtimeStage $relativePath
    if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
        throw "Skill runtime is incomplete: $candidate"
    }
}

Write-Host "Skill compatibility: $env:HITK_SKILL_COMPAT"
if (-not [string]::IsNullOrWhiteSpace($env:HITK_DATA_DIR)) {
    Write-Host "HitK data directory: $env:HITK_DATA_DIR"
}
if (-not [string]::IsNullOrWhiteSpace($env:HITK_RUNTIME_ROOT)) {
    Write-Host "Runtime root override: $env:HITK_RUNTIME_ROOT"
} else {
    Write-Host "Runtime root: $runtimeStage (the development build copies it to bin\runtime)"
}

if ($ValidateOnly) {
    Write-Host 'Development environment is ready.'
    exit 0
}

Push-Location $repoRoot
try {
    & wails3 dev -config (Join-Path $repoRoot 'build\config.yml') -port $VitePort
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
} finally {
    Pop-Location
}
