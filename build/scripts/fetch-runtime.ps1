param(
    [ValidateSet('Fetch', 'Stage', 'Clean', 'Component', 'Publish')]
    [string]$Action = 'Fetch',
    [string]$ManifestPath = (Join-Path $PSScriptRoot '..\runtime-manifest.json'),
    [string]$StageDirectory = (Join-Path $PSScriptRoot '..\windows\runtime-stage'),
    [string]$ComponentDirectory = (Join-Path $PSScriptRoot '..\windows\component-stage\document'),
    [string]$ComponentOutputDirectory = (Join-Path $PSScriptRoot '..\output'),
    [string]$ComponentRepository = 'AntNoHuabei/HitKEnv'
)

$ErrorActionPreference = 'Stop'
$manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
$cacheRoot = $env:HITK_RUNTIME_BUILD_CACHE
if ([string]::IsNullOrWhiteSpace($cacheRoot)) {
    $cacheRoot = Join-Path $env:LOCALAPPDATA 'HitK\build-cache\runtimes'
}
$platformCache = Join-Path $cacheRoot $manifest.platform

function Get-ArchivePath($runtime) {
    $extension = if ($runtime.download.archiveType -eq 'tar.gz') { '.tar.gz' } else { '.zip' }
    return Join-Path $platformCache ("{0}-{1}-{2}{3}" -f $runtime.name, $runtime.version, $runtime.download.sha256, $extension)
}

function Get-SHA256([string]$path) {
    $stream = [IO.File]::OpenRead($path)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '').ToLowerInvariant()
    } finally {
        $sha.Dispose()
        $stream.Dispose()
    }
}

function Assert-Archive($runtime, [string]$path) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { return $false }
    $actual = Get-SHA256 $path
    if ($actual -ne $runtime.download.sha256.ToLowerInvariant()) {
        Remove-Item -LiteralPath $path -Force
        throw "Runtime checksum mismatch for $($runtime.name): $actual"
    }
    return $true
}

function Fetch-Archive($runtime) {
    New-Item -ItemType Directory -Path $platformCache -Force | Out-Null
    $target = Get-ArchivePath $runtime
    if (Assert-Archive $runtime $target) { return $target }
    $partial = "$target.partial"
    Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
    try {
        Write-Host "Downloading $($runtime.name) $($runtime.version)..."
        $request = @{ Uri = $runtime.download.url; OutFile = $partial; UseBasicParsing = $true }
        $proxy = if (-not [string]::IsNullOrWhiteSpace($env:HTTPS_PROXY)) { $env:HTTPS_PROXY } else { $env:HTTP_PROXY }
        if (-not [string]::IsNullOrWhiteSpace($proxy)) { $request.Proxy = $proxy }
        Invoke-WebRequest @request
        if (-not (Assert-Archive $runtime $partial)) { throw 'Downloaded runtime is missing' }
        Move-Item -LiteralPath $partial -Destination $target -Force
        return $target
    } finally {
        Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
    }
}

if ($Action -eq 'Clean') {
    $resolvedStage = [IO.Path]::GetFullPath($StageDirectory)
    $resolvedBuild = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
    if (-not $resolvedStage.StartsWith($resolvedBuild, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to clean outside build directory: $resolvedStage"
    }
    Remove-Item -LiteralPath $resolvedStage -Recurse -Force -ErrorAction SilentlyContinue
    exit 0
}

$archives = @{}
foreach ($runtime in $manifest.runtimes) { $archives[$runtime.name] = Fetch-Archive $runtime }
if ($Action -eq 'Fetch') { exit 0 }

if ($Action -eq 'Publish') {
    $component = @($manifest.components | Where-Object { $_.skill -eq 'document' }) | Select-Object -First 1
    if ($null -eq $component) { throw 'document component is missing from the runtime manifest' }
    if ([string]::IsNullOrWhiteSpace($env:MODELSCOPE_API_TOKEN)) {
        throw 'MODELSCOPE_API_TOKEN is required to publish a runtime component'
    }
    $archive = Join-Path $ComponentOutputDirectory ([IO.Path]::GetFileName(([Uri]$component.url).AbsolutePath))
    if (-not (Test-Path -LiteralPath $archive -PathType Leaf)) {
        throw "Component archive not found: $archive"
    }
    $pythonCommand = (Get-Command python -ErrorAction Stop).Source
    & $pythonCommand -c "import os; from modelscope.hub.api import HubApi; HubApi(token=os.environ['MODELSCOPE_API_TOKEN']).upload_file(path_or_fileobj=r'$archive', path_in_repo='runtime/$([IO.Path]::GetFileName($archive))', repo_id='$ComponentRepository', repo_type='dataset', commit_message='add HitK document runtime component')"
    if ($LASTEXITCODE -ne 0) { throw 'ModelScope component upload failed' }
    Write-Host "Published component to $ComponentRepository/runtime"
    exit 0
}

if ($Action -eq 'Component') {
    $uv = Join-Path $StageDirectory 'uv\uv.exe'
    $python = Join-Path $StageDirectory 'python\python.exe'
    if (-not (Test-Path -LiteralPath $uv -PathType Leaf) -or -not (Test-Path -LiteralPath $python -PathType Leaf)) {
        throw 'Run the Stage action before packaging the document component'
    }
    $resolvedComponent = [IO.Path]::GetFullPath($ComponentDirectory)
    $resolvedBuild = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
    if (-not $resolvedComponent.StartsWith($resolvedBuild, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to clean outside build directory: $resolvedComponent"
    }
    Remove-Item -LiteralPath $resolvedComponent -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Path $resolvedComponent -Force | Out-Null

    $skillSource = Join-Path $PSScriptRoot '..\..\skills\document'
    $requirements = Join-Path ([IO.Path]::GetTempPath()) ("hitk-document-" + [Guid]::NewGuid().ToString('N') + '.txt')
    try {
        $env:UV_PYTHON = $python
        $env:UV_NO_MANAGED_PYTHON = 'true'
        $env:UV_PYTHON_DOWNLOADS = 'never'
        & $uv export --quiet --project $skillSource --frozen --no-dev --no-emit-project --format requirements-txt --output-file $requirements
        if ($LASTEXITCODE -ne 0) { throw 'uv export failed for document component' }
        & $uv pip install --python $python --target (Join-Path $resolvedComponent 'site-packages') --requirement $requirements --link-mode copy
        if ($LASTEXITCODE -ne 0) { throw 'uv install failed for document component' }

        $modelCache = Join-Path $platformCache 'document-models'
        New-Item -ItemType Directory -Path $modelCache -Force | Out-Null
        $env:DOCLING_CACHE_DIR = $modelCache
        $env:PYTHONPATH = Join-Path $resolvedComponent 'site-packages'
        & $python -B (Join-Path $skillSource 'prefetch_models.py')
        if ($LASTEXITCODE -ne 0) { throw 'PP-OCRv6 model prefetch failed' }
        Copy-Item -LiteralPath $modelCache -Destination (Join-Path $resolvedComponent 'models-cache') -Recurse -Force
    } finally {
        Remove-Item -LiteralPath $requirements -Force -ErrorAction SilentlyContinue
        Remove-Item Env:PYTHONPATH -ErrorAction SilentlyContinue
    }

    $skillManifest = Get-Content -LiteralPath (Join-Path $skillSource 'skill.json') -Raw | ConvertFrom-Json
    $componentName = "document-windows-amd64-$($skillManifest.version).zip"
    New-Item -ItemType Directory -Path $ComponentOutputDirectory -Force | Out-Null
    $componentArchive = Join-Path $ComponentOutputDirectory $componentName
    Remove-Item -LiteralPath $componentArchive -Force -ErrorAction SilentlyContinue
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [IO.Compression.ZipFile]::CreateFromDirectory(
        $resolvedComponent,
        [IO.Path]::GetFullPath($componentArchive),
        [IO.Compression.CompressionLevel]::Optimal,
        $false
    )

    $componentFile = Get-Item -LiteralPath $componentArchive
    $unpackedBytes = (Get-ChildItem -LiteralPath $resolvedComponent -Recurse -File | Measure-Object Length -Sum).Sum
    $componentUrl = "https://www.modelscope.cn/datasets/$ComponentRepository/resolve/master/runtime/$componentName"
    $component = @($manifest.components | Where-Object { $_.skill -eq 'document' }) | Select-Object -First 1
    if ($null -eq $component) {
        $component = [pscustomobject]@{}
        $manifest.components = @($component)
    }
    $component.id = 'document-docling'
    $component.skill = 'document'
    $component.version = [string]$skillManifest.version
    $component.platform = $manifest.platform
    $component.source = "ModelScope - $ComponentRepository"
    $component.url = $componentUrl
    $component.sha256 = Get-SHA256 $componentArchive
    $component.archiveType = 'zip'
    $component.compressedBytes = [int64]$componentFile.Length
    $component.unpackedBytes = [int64]$unpackedBytes
    $component.installDirectory = 'site-packages'
    $component.requiredRuntimes = [pscustomobject]@{ python = '3.12.14' }

    $manifestJson = $manifest | ConvertTo-Json -Depth 10
    [IO.File]::WriteAllText([IO.Path]::GetFullPath($ManifestPath), $manifestJson, (New-Object Text.UTF8Encoding($false)))
    Write-Host "Document component ready: $componentArchive"
    Write-Host "SHA256: $($component.sha256)"
    Write-Host "Compressed: $($component.compressedBytes) bytes"
    Write-Host "Unpacked: $($component.unpackedBytes) bytes"
    exit 0
}

Remove-Item -LiteralPath $StageDirectory -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $StageDirectory -Force | Out-Null
foreach ($runtime in $manifest.runtimes) {
    $extract = Join-Path ([IO.Path]::GetTempPath()) ("hitk-runtime-" + [Guid]::NewGuid().ToString('N'))
    try {
        New-Item -ItemType Directory -Path $extract -Force | Out-Null
        if ($runtime.download.archiveType -eq 'tar.gz') {
            & tar -xf $archives[$runtime.name] -C $extract
            if ($LASTEXITCODE -ne 0) { throw "tar failed for $($runtime.name)" }
        } else {
            Expand-Archive -LiteralPath $archives[$runtime.name] -DestinationPath $extract -Force
        }
        $source = if ($runtime.download.archiveRoot -eq '.') { $extract } else { Join-Path $extract $runtime.download.archiveRoot }
        if (-not (Test-Path -LiteralPath $source -PathType Container)) { throw "Archive root not found: $source" }
        $destination = Join-Path $StageDirectory $runtime.name
        New-Item -ItemType Directory -Path $destination -Force | Out-Null
        Get-ChildItem -LiteralPath $source -Force | Copy-Item -Destination $destination -Recurse -Force
        Set-Content -LiteralPath (Join-Path $destination 'VERSION') -Value $runtime.version -NoNewline -Encoding ascii
    } finally {
        Remove-Item -LiteralPath $extract -Recurse -Force -ErrorAction SilentlyContinue
    }
}

$skillSource = Join-Path $PSScriptRoot '..\..\skills\document'
$skillTarget = Join-Path $StageDirectory 'skills\document'
New-Item -ItemType Directory -Path $skillTarget -Force | Out-Null
Get-ChildItem -LiteralPath $skillSource -Force | Where-Object { $_.Name -notin @('site-packages', 'models-cache') } | Copy-Item -Destination $skillTarget -Recurse -Force

$stagedManifest = Join-Path $StageDirectory 'runtime-manifest.json'
Copy-Item -LiteralPath $ManifestPath -Destination $stagedManifest -Force
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
Push-Location $repoRoot
try {
    $digestOutput = & go run .\build\scripts\runtime-digest ([IO.Path]::GetFullPath($StageDirectory))
    if ($LASTEXITCODE -ne 0) { throw 'Runtime payload digest failed' }
    $digest = $digestOutput | Select-Object -Last 1
    if ([string]::IsNullOrWhiteSpace($digest)) { throw 'Runtime payload digest was empty' }
    $manifest.payloadDigest = $digest.Trim()
} finally { Pop-Location }
$manifestJson = $manifest | ConvertTo-Json -Depth 8
[IO.File]::WriteAllText($stagedManifest, $manifestJson, (New-Object Text.UTF8Encoding($false)))
Write-Host "Runtime staging ready: $StageDirectory"
