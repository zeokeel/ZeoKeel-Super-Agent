# ZeoKeel install for Windows.
#
# There is no installer framework here on purpose: a MSI/NSIS build needs a Windows
# toolchain in the release pipeline, and an agent that already connects to your chat
# should not also ask for elevated trust. This copies one exe, makes one shortcut,
# and leaves an uninstaller next to it.
#
#   powershell -ExecutionPolicy Bypass -File install.ps1
param(
    [string]$InstallDir = "$env:LOCALAPPDATA\ZeoKeel"
)

$ErrorActionPreference = "Stop"
$source = Join-Path $PSScriptRoot "zeokeel.exe"
if (-not (Test-Path $source)) { throw "zeokeel.exe not found next to install.ps1" }

Write-Host "Installing ZeoKeel to $InstallDir"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item -Force $source (Join-Path $InstallDir "zeokeel.exe")
Copy-Item -Force (Join-Path $PSScriptRoot "CHANGELOG.md") (Join-Path $InstallDir "CHANGELOG.md")
if (Test-Path (Join-Path $PSScriptRoot "zeokeel.ico")) {
    Copy-Item -Force (Join-Path $PSScriptRoot "zeokeel.ico") (Join-Path $InstallDir "zeokeel.ico")
}

# Start Menu shortcut: the only discoverability a single exe needs.
$startMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
$lnk = Join-Path $startMenu "ZeoKeel.lnk"
$ws = New-Object -ComObject WScript.Shell
$sc = $ws.CreateShortcut($lnk)
$sc.TargetPath = Join-Path $InstallDir "zeokeel.exe"
$sc.WorkingDirectory = $InstallDir
$sc.Description = "ZeoKeel Super Agent"
$sc.IconLocation = Join-Path $InstallDir "zeokeel.ico"
$sc.Save()
Write-Host "Start Menu shortcut: $lnk"

# Uninstaller written beside the binary, so removing it is as simple as it should be.
$uninstall = @"
`$ErrorActionPreference = "Stop"
`$dir = "$InstallDir"
`$lnk = Join-Path `$env:APPDATA "Microsoft\Windows\Start Menu\Programs\ZeoKeel.lnk"
if (Test-Path `$lnk) { Remove-Item -Force `$lnk }
if (Test-Path `$dir) { Remove-Item -Recurse -Force `$dir }
Write-Host "ZeoKeel removed. Your data is in `$env:USERPROFILE\.zeokeel - delete it yourself or keep your history."
"@
Set-Content -Encoding UTF8 (Join-Path $InstallDir "uninstall.ps1") $uninstall

Write-Host ""
Write-Host "Done. Start it from the Start Menu, then finish the first-run setup."
Write-Host "Data lives in $env:USERPROFILE\.zeokeel"
Write-Host "Uninstall: powershell -ExecutionPolicy Bypass -File `"$InstallDir\uninstall.ps1`""
