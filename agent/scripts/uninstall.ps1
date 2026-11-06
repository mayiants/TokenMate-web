# Woodgit Agent — uninstall (user level). Restores nothing by itself:
# run `woodgit-agent restore` first if the client config should be reverted.
param([switch]$KeepState)

$ErrorActionPreference = "Stop"
$InstallDir = Join-Path $env:LOCALAPPDATA "WoodgitAgent"
$TaskName = "WoodgitAgent Heartbeat"

$existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($existing) {
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    Write-Host "Removed scheduled task '$TaskName'."
}

if (Test-Path $InstallDir) {
    if ($KeepState) {
        Get-ChildItem $InstallDir -Filter "woodgit-agent.exe" | Remove-Item -Force
        Write-Host "Removed binary; state and secrets kept in $InstallDir."
    } else {
        Remove-Item -Recurse -Force $InstallDir
        Write-Host "Removed $InstallDir (state, secrets, backups)."
    }
}

Write-Host "Done. The device-bound key on the server was NOT revoked —"
Write-Host "ask your administrator to remove the device if this PC is decommissioned."
