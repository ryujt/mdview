[CmdletBinding()]
param([string]$IsccPath)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Invoke-Checked {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Command exited with code $LASTEXITCODE" }
}

function Write-Utf8 {
    param([string]$Path, [string]$Value)
    [IO.File]::WriteAllText($Path, $Value, (New-Object Text.UTF8Encoding($false)))
}

$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Push-Location $projectRoot
$buildWork = $null
$oldBuildEnvironment = @{}
foreach ($key in @('CGO_ENABLED', 'GOOS', 'GOARCH')) {
    $oldBuildEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
}
try {
    if ($env:OS -ne 'Windows_NT') { throw 'Build this release on Windows.' }
    foreach ($command in @('go', 'node')) { $null = Get-Command $command -ErrorAction Stop }
    if (-not $IsccPath) {
        $isccCommand = Get-Command ISCC.exe -ErrorAction SilentlyContinue
        if ($isccCommand) { $IsccPath = $isccCommand.Source }
        else {
            foreach ($base in @($env:ProgramFiles, ${env:ProgramFiles(x86)}, "$env:LOCALAPPDATA\Programs")) {
                foreach ($edition in @('Inno Setup 7', 'Inno Setup 6')) {
                    $candidate = Join-Path $base "$edition\ISCC.exe"
                    if (Test-Path -LiteralPath $candidate) { $IsccPath = $candidate; break }
                }
                if ($IsccPath) { break }
            }
        }
    }
    if (-not $IsccPath -or -not (Test-Path -LiteralPath $IsccPath)) {
        throw 'Install Inno Setup 6.3+ or 7, or supply -IsccPath.'
    }
    $IsccPath = (Resolve-Path -LiteralPath $IsccPath).Path
    $version = (Get-Content -Raw VERSION).Trim()
    if ($version -notmatch '^\d+\.\d+\.\d+$') { throw 'VERSION must be major.minor.patch.' }
    $name = "mdview-$version-windows-x64"
    $release = Join-Path $projectRoot "dist\$name"
    if (Test-Path -LiteralPath $release) { throw "Output already exists: $release. Move it aside before rebuilding." }
    $commit = 'unknown'
    if (Get-Command git -ErrorAction SilentlyContinue) {
        $revision = & git rev-parse --short=12 HEAD 2>$null
        if ($LASTEXITCODE -eq 0) {
            $commit = "$revision"
            if (& git status --porcelain -- .) { $commit += '-dirty' }
        }
    }
    $buildDate = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    # Keep tests on the host architecture; set the release target only afterwards.
    [Environment]::SetEnvironmentVariable('GOOS', 'windows', 'Process')
    [Environment]::SetEnvironmentVariable('GOARCH', $null, 'Process')
    Write-Host 'Checking Go and JavaScript...'
    Invoke-Checked go @('test', './...')
    Invoke-Checked go @('vet', './...')
    $jsTests = @(Get-ChildItem test -Filter '*.test.mjs' | ForEach-Object FullName)
    Invoke-Checked node (@('--test') + $jsTests)

    $null = New-Item -ItemType Directory -Force (Join-Path $projectRoot 'dist')
    $buildWork = Join-Path $projectRoot ("dist\.windows-build-" + [Guid]::NewGuid().ToString('N'))
    $payload = Join-Path $buildWork 'payload'
    $output = Join-Path $buildWork $name
    $null = New-Item -ItemType Directory $payload, $output
    [Environment]::SetEnvironmentVariable('CGO_ENABLED', '0', 'Process')
    [Environment]::SetEnvironmentVariable('GOARCH', 'amd64', 'Process')
    Write-Host "Building $name..."
    Invoke-Checked go @('build', '-trimpath', '-buildvcs=false',
        "-ldflags=-s -w -X main.version=$version -X main.commit=$commit -X main.buildDate=$buildDate",
        '-o', (Join-Path $payload 'mdview.exe'), './cmd/')
    Invoke-Checked (Join-Path $payload 'mdview.exe') @('--version')
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'INSTALL.txt') -Destination $payload
    Copy-Item -LiteralPath 'config.example.json' -Destination $payload

    # Collect licenses without requiring Python or including local credentials.
    Invoke-Checked go @('mod', 'download', 'all')
    $goRoot = & go env GOROOT
    if ($LASTEXITCODE -ne 0) { throw 'Cannot resolve GOROOT.' }
    $goVersion = & go env GOVERSION
    if ($LASTEXITCODE -ne 0) { throw 'Cannot resolve GOVERSION.' }
    $notices = New-Object Text.StringBuilder
    $null = $notices.AppendLine("mdview - Third-party notices`n`nGo $goVersion`n")
    $null = $notices.AppendLine([IO.File]::ReadAllText((Join-Path $goRoot 'LICENSE')))
    $modules = & go list -m -f '{{if not .Main}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' all
    if ($LASTEXITCODE -ne 0) { throw 'Cannot list module licenses.' }
    foreach ($module in $modules) {
        if (-not $module.Trim()) { continue }
        $fields = $module -split '\|', 3
        $licenses = @('LICENSE', 'LICENSE.txt', 'LICENSE.md', 'COPYING') |
            ForEach-Object { Join-Path $fields[2] $_ } | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf }
        if (-not $licenses) { throw "License missing for $($fields[0])." }
        $null = $notices.AppendLine("`n========================================================================`n$($fields[0]) $($fields[1])`n")
        foreach ($license in $licenses) { $null = $notices.AppendLine([IO.File]::ReadAllText($license)) }
    }
    Write-Utf8 (Join-Path $payload 'THIRD-PARTY-NOTICES.txt') $notices.ToString()
    Invoke-Checked $IsccPath @('/Qp', "/DAppVersion=$version", "/DPayloadDir=$payload", "/DReleaseDir=$output",
        (Join-Path $PSScriptRoot 'mdview.iss'))
    Compress-Archive -Path "$payload\*" -DestinationPath (Join-Path $output "$name-portable.zip")
    Copy-Item -LiteralPath (Join-Path $payload 'INSTALL.txt'), (Join-Path $payload 'THIRD-PARTY-NOTICES.txt') -Destination $output
    $manifest = [ordered]@{
        version = $version; platform = 'windows'; architecture = 'amd64'; minimum_windows = '10'
        commit = $commit; build_date = $buildDate; authenticode_signed = $false
        installer = 'Inno Setup'; compiler = Split-Path (Split-Path $IsccPath -Parent) -Leaf
        install_scope = 'current_user'; default_install_path = '%LOCALAPPDATA%\Programs\mdview'
    }
    Write-Utf8 (Join-Path $output 'release.json') (($manifest | ConvertTo-Json) + "`n")
    $checksums = Get-ChildItem -LiteralPath $output -File | Sort-Object Name | ForEach-Object {
        "{0}  {1}" -f (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant(), $_.Name
    }
    Write-Utf8 (Join-Path $output 'SHA256SUMS') (($checksums -join "`n") + "`n")
    Move-Item -LiteralPath $output -Destination $release
    Write-Host "Complete: $release"
}
finally {
    foreach ($key in $oldBuildEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($key, $oldBuildEnvironment[$key], 'Process')
    }
    if ($buildWork -and (Test-Path -LiteralPath $buildWork)) {
        $resolvedWork = (Resolve-Path -LiteralPath $buildWork).Path
        $allowedParent = Join-Path $projectRoot 'dist'
        if ((Split-Path $resolvedWork -Parent) -ne $allowedParent -or
            (Split-Path $resolvedWork -Leaf) -notmatch '^\.windows-build-[a-f0-9]{32}$') {
            throw "Refusing to remove unexpected staging path: $resolvedWork"
        }
        Remove-Item -LiteralPath $resolvedWork -Recurse -Force
    }
    Pop-Location
}
