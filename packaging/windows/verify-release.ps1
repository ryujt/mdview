[CmdletBinding()]
param([string]$ReleaseDir)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
if (-not $ReleaseDir) {
    $version = (Get-Content -Raw (Join-Path $projectRoot 'VERSION')).Trim()
    $ReleaseDir = Join-Path $projectRoot "dist\mdview-$version-windows-x64"
}
$ReleaseDir = (Resolve-Path -LiteralPath $ReleaseDir).Path
$manifest = Get-Content -Raw -LiteralPath (Join-Path $ReleaseDir 'release.json') | ConvertFrom-Json
$name = "mdview-$($manifest.version)-windows-x64"
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{1EF7F0CF-B28A-4EEB-9949-1CF8046C169B}_is1'
if (Test-Path -LiteralPath $uninstallKey) { throw 'An existing mdview installation must not be modified by this verification.' }

function Get-UserPath {
    [Environment]::GetEnvironmentVariable('Path', 'User')
}
function Run-Installer {
    param([string]$File, [string[]]$Options)
    $process = Start-Process -FilePath $File -ArgumentList $Options -PassThru -Wait -WindowStyle Hidden
    if ($process.ExitCode -ne 0) { throw "Installer exited with code $($process.ExitCode)." }
}

$pathBefore = Get-UserPath
$work = Join-Path $projectRoot ("dist\.windows-verify-" + [Guid]::NewGuid().ToString('N'))
$install = Join-Path $work 'installed app'
$portable = Join-Path $work 'portable'
$group = 'mdview'
$shortcutGroup = Join-Path ([Environment]::GetFolderPath('StartMenu')) "Programs\$group"
if (Test-Path -LiteralPath $shortcutGroup) { throw 'Existing mdview shortcuts must not be modified by verification.' }
$null = New-Item -ItemType Directory $work
$verified = $false
try {
    foreach ($line in Get-Content -LiteralPath (Join-Path $ReleaseDir 'SHA256SUMS')) {
        if ($line -notmatch '^([a-f0-9]{64})  (.+)$') { throw 'Malformed checksum entry.' }
        if ((Get-FileHash -LiteralPath (Join-Path $ReleaseDir $Matches[2])).Hash.ToLowerInvariant() -ne $Matches[1]) {
            throw "Checksum mismatch: $($Matches[2])"
        }
    }
    Expand-Archive -LiteralPath (Join-Path $ReleaseDir "$name-portable.zip") -DestinationPath $portable
    $expectedFiles = @('config.example.json', 'INSTALL.txt', 'mdview.exe', 'THIRD-PARTY-NOTICES.txt')
    $actualFiles = @(Get-ChildItem -LiteralPath $portable -File | Sort-Object Name | ForEach-Object Name)
    if (Compare-Object $expectedFiles $actualFiles) { throw 'Unexpected portable payload.' }
    $portableVersion = & (Join-Path $portable 'mdview.exe') --version
    if ($LASTEXITCODE -ne 0 -or $portableVersion -notlike "mdview $($manifest.version) *") { throw 'Portable version check failed.' }

    $setup = Join-Path $ReleaseDir "$name-setup.exe"
    $options = @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/SP-', '/LANG=english',
        "/DIR=`"$install`"", '/TASKS=addtopath', "/LOG=`"$work\install.log`"")
    Run-Installer $setup $options
    $installedVersion = & (Join-Path $install 'mdview.exe') --version
    if ($LASTEXITCODE -ne 0 -or $installedVersion -ne $portableVersion) { throw 'Installed version differs from portable version.' }
    if (-not (Test-Path -LiteralPath $uninstallKey)) { throw 'Missing uninstall registration.' }
    $shortcut = Join-Path ([Environment]::GetFolderPath('StartMenu')) "Programs\$group\mdview.lnk"
    if (-not (Test-Path -LiteralPath $shortcut)) { throw 'Missing Start menu shortcut.' }
    # Reinstall exercises the stable AppId, upgrade behavior and PATH deduplication.
    Run-Installer $setup $options
    $entries = @((Get-UserPath) -split ';' | Where-Object { $_ -eq $install })
    if ($entries.Count -ne 1) { throw 'PATH entry missing or duplicated after reinstall.' }
    Run-Installer (Join-Path $install 'unins000.exe') @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART',
        "/LOG=`"$work\uninstall.log`"")
    # The uninstaller may delegate deletion of its own executable to a helper.
    if (Test-Path -LiteralPath (Join-Path $install 'mdview.exe')) { throw 'Executable remains after uninstall.' }
    if (Test-Path -LiteralPath $shortcut) { throw 'Shortcut remains after uninstall.' }
    if (Test-Path -LiteralPath $uninstallKey) { throw 'Uninstall registration remains.' }
    if ((Get-UserPath) -cne $pathBefore) { throw 'User PATH was not restored exactly.' }
    $verified = $true
    Write-Host 'Verified: checksums, portable payload/version, install, shortcut, reinstall, PATH deduplication and uninstall.'
}
finally {
    if (Test-Path -LiteralPath (Join-Path $install 'unins000.exe')) {
        Run-Installer (Join-Path $install 'unins000.exe') @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART')
    }
    if ($verified) {
        $resolvedWork = (Resolve-Path -LiteralPath $work).Path
        if ((Split-Path $resolvedWork -Parent) -ne (Join-Path $projectRoot 'dist') -or
            (Split-Path $resolvedWork -Leaf) -notmatch '^\.windows-verify-[a-f0-9]{32}$') {
            throw "Refusing to remove unexpected verification path: $resolvedWork"
        }
        Remove-Item -LiteralPath $resolvedWork -Recurse -Force
    }
    else { Write-Host "Verification logs retained in $work" }
}
