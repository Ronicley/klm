param(
    [Parameter(Mandatory)]
    [ValidatePattern('^\d+\.\d+\.\d+$')]
    [string]$Version
)
$ErrorActionPreference = 'Stop'
$releaseVersion = [version]$Version
if ($releaseVersion.Major -gt 65535 -or $releaseVersion.Minor -gt 65535 -or $releaseVersion.Build -gt 65535) {
    throw 'Windows version components must fit in 16 bits.'
}
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$desktop = Join-Path $root 'clients\desktop'
$releaseDir = Join-Path $root 'dist\release'
New-Item -ItemType Directory -Path $releaseDir -Force | Out-Null
$config = Join-Path $root 'dist\tauri-release.json'
[IO.File]::WriteAllText($config, (@{ version = $Version } | ConvertTo-Json))

# Build desktop first: Tauri downloads the NSIS compiler reused by the engine build.
$previousDesktopVersion = $env:VITE_DESKTOP_VERSION
$env:VITE_DESKTOP_VERSION = $Version
Push-Location $desktop
try {
    & npm.cmd run desktop:build -- --config $config -- --locked
    if ($LASTEXITCODE -ne 0) { throw 'Desktop installer build failed.' }
} finally {
    Pop-Location
    $env:VITE_DESKTOP_VERSION = $previousDesktopVersion
}
& (Join-Path $PSScriptRoot 'build-engine.ps1') -Version $Version

$desktopInstaller = Join-Path $desktop "src-tauri\target\release\bundle\nsis\KLM Desktop_${Version}_x64-setup.exe"
$engineInstaller = Join-Path $root "dist\KLM-Engine-${Version}-x64-setup.exe"
$artifacts = @(
    @{ Source = $engineInstaller; Name = "KLM-Engine-${Version}-x64-setup.exe" },
    @{ Source = $desktopInstaller; Name = "KLM-Desktop-${Version}-x64-setup.exe" }
)
$hashes = foreach ($artifact in $artifacts) {
    if (-not (Test-Path -LiteralPath $artifact.Source -PathType Leaf)) {
        throw "Expected installer missing: $($artifact.Source)"
    }
    $destination = Join-Path $releaseDir $artifact.Name
    Copy-Item -LiteralPath $artifact.Source -Destination $destination -Force
    "$((Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant())  $($artifact.Name)"
}
$hashes | Set-Content -LiteralPath (Join-Path $releaseDir 'SHA256SUMS.txt') -Encoding ascii
Write-Output "Release installers: $releaseDir"
