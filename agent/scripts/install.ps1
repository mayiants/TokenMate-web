# Woodgit Agent — user-level install (no admin rights required).
# Copies the agent into %LOCALAPPDATA%\WoodgitAgent and registers a
# per-user scheduled task that runs `heartbeat` every 5 minutes.
#
# Usage:  powershell -ExecutionPolicy Bypass -File install.ps1 [-Binary .\woodgit-agent.exe]
param(
    [string]$Binary = ".\woodgit-agent.exe",
    [string]$Server = "",
    [switch]$Insecure
)

$ErrorActionPreference = "Stop"
$InstallDir = Join-Path $env:LOCALAPPDATA "WoodgitAgent"
$TaskName = "WoodgitAgent Heartbeat"

if (-not (Test-Path $Binary)) { throw "Agent binary not found: $Binary" }

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item $Binary (Join-Path $InstallDir "woodgit-agent.exe") -Force

# Heartbeat every 5 minutes, starting at user logon. A per-user task avoids
# any UAC prompt and stops with the user's session context.
$exe = Join-Path $InstallDir "woodgit-agent.exe"
$action = New-ScheduledTaskAction -Execute $exe -Argument "heartbeat"
$trigger = New-ScheduledTaskTrigger -Once -At (Get-Date) -RepetitionInterval (New-TimeSpan -Minutes 5) -RepetitionDuration (New-TimeSpan -Days 3650)
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable

$existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($existing) { Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false }
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings | Out-Null

# User PATH so `woodgit-agent` works from any terminal.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$InstallDir", "User")
}

Write-Host "Installed to $InstallDir"
Write-Host "Heartbeat scheduled task '$TaskName' registered (every 5 min)."
Write-Host ""
Write-Host "Next step — enroll this device:"
$args = "enroll --server $Server"
if ($Insecure) { $args += " --insecure" }
Write-Host "  woodgit-agent $args"
Write-Host "(open a NEW terminal so PATH picks up, or use $exe directly)"
