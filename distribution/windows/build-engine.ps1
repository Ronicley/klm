param([string]$MakeNSIS, [ValidatePattern('^\d+\.\d+\.\d+$')][string]$Version = '0.2.1')
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
if (-not (Test-Path -LiteralPath (Join-Path $root 'engine\go.mod'))) { throw 'Engine source directory not found.' }

if (-not $MakeNSIS) {
    $command = Get-Command makensis.exe -ErrorAction SilentlyContinue
    if ($command) { $MakeNSIS = $command.Source }
    else {
        # Reuse Tauri's downloaded compiler, or a regular NSIS installation.
        foreach ($candidate in @(
            (Join-Path $env:LOCALAPPDATA 'tauri\NSIS\makensis.exe'),
            (Join-Path ${env:ProgramFiles(x86)} 'NSIS\makensis.exe')
        )) {
            if (Test-Path -LiteralPath $candidate) { $MakeNSIS = $candidate; break }
        }
    }
}
if (-not $MakeNSIS -or -not (Test-Path -LiteralPath $MakeNSIS)) {
    throw 'NSIS not found. Build the desktop bundle first, install NSIS 3, or pass -MakeNSIS with its executable path.'
}

$dist = Join-Path $root 'dist'
if (-not (Test-Path -LiteralPath $root)) { throw 'Repository root not found.' }
New-Item -ItemType Directory -Path $dist -Force | Out-Null
$output = Join-Path $dist 'engine'
New-Item -ItemType Directory -Path $output -Force | Out-Null
& go -C (Join-Path $root 'engine') build -trimpath -o (Join-Path $output 'klm.exe') .
if ($LASTEXITCODE -ne 0) { throw 'Engine build failed.' }
Copy-Item -LiteralPath (Join-Path $root 'engine\prompts') -Destination $output -Recurse -Force
& $MakeNSIS '/V3' "/DVERSION=$Version" (Join-Path $PSScriptRoot 'engine.nsi')
if ($LASTEXITCODE -ne 0) { throw 'Engine installer build failed.' }
Write-Output (Join-Path $dist "KLM-Engine-$Version-x64-setup.exe")
