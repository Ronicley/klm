# Installer/hidden-login-launcher helper, not a public CLI command.
param([Parameter(Mandatory)][ValidateSet('Install', 'Uninstall', 'Start')][string]$Action)
$ErrorActionPreference = 'Stop'
$installDir = $PSScriptRoot.TrimEnd('\')

if ($Action -eq 'Start') {
    # Login/installer environments can predate a PATH update. Refresh from the
    # user's real registry environment so Git, Node and harness discovery work.
    $machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $user = [Environment]::GetEnvironmentVariable('Path', 'User')
    $env:Path = [Environment]::ExpandEnvironmentVariables("$machine;$user")
    & (Join-Path $installDir 'klm.exe') start
    exit $LASTEXITCODE
}

$environment = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
$registration = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Software\KLM\Engine')
$run = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Software\Microsoft\Windows\CurrentVersion\Run')
try {
    # Preserve unexpanded variables and registry type; never truncate PATH via NSIS.
    $path = [string]$environment.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
    if ($environment.GetValueNames() -contains 'Path') { $kind = $environment.GetValueKind('Path') }
    $entries = @($path -split ';')
    if ($Action -eq 'Install') {
        # Put the current CLI first so stale MVP/legacy `klm.exe` installs later
        # on PATH cannot shadow `klm start` or `klm stop`.
        $remaining = @($entries | Where-Object { $_.Trim().Trim('"').TrimEnd('\') -ine $installDir })
        $newPath = (@($installDir) + $remaining | Where-Object { $_ } | Select-Object -Unique) -join ';'
        if ($newPath -cne $path) { $environment.SetValue('Path', $newPath, $kind) }
        $registration.SetValue('AddedPath', $installDir)
        $launcher = Join-Path $installDir 'start-engine.vbs'
        $wscript = Join-Path $env:SystemRoot 'System32\wscript.exe'
        $run.SetValue('KLM Engine', "`"$wscript`" `"$launcher`"")
    } else {
        if ($registration.GetValue('AddedPath', '') -ieq $installDir) {
            $remaining = @($entries | Where-Object { $_.Trim().Trim('"').TrimEnd('\') -ine $installDir })
            $environment.SetValue('Path', ($remaining -join ';'), $kind)
            $registration.DeleteValue('AddedPath', $false)
        }
        $run.DeleteValue('KLM Engine', $false)
    }
} finally {
    $run.Dispose()
    $registration.Dispose()
    $environment.Dispose()
}

# WM_SETTINGCHANGE broadcasts wait separately for every top-level window. Never
# hold installation/uninstallation (or nsExec's output pipe) while those reply.
# Pass a self-contained command: uninstall may immediately remove this script.
$notification = {
    Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class EnvironmentNotification { [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)] public static extern IntPtr SendMessageTimeout(IntPtr window, uint message, UIntPtr wParam, string lParam, uint flags, uint timeout, out UIntPtr result); }'
    $result = [UIntPtr]::Zero
    [EnvironmentNotification]::SendMessageTimeout([IntPtr]0xffff, 0x001a, [UIntPtr]::Zero, 'Environment', 2, 1000, [ref]$result) > $null
}
try {
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($notification.ToString()))
    $start.Arguments = "-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand $encoded"
    # ShellExecute avoids inheriting the installer's redirected stdout/stderr.
    $start.UseShellExecute = $true
    $start.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
    $process = [Diagnostics.Process]::Start($start)
    $process.Dispose()
} catch {
    Write-Warning 'Environment saved, but Windows could not be notified. Open a new terminal or sign in again to refresh PATH.'
}
