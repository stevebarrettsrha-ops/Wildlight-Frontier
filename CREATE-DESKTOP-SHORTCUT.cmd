@echo off
setlocal
set "WILDLIGHT_GAME_DIR=%~dp0"
powershell.exe -NoLogo -NoProfile -Command "$ErrorActionPreference='Stop'; $root=$env:WILDLIGHT_GAME_DIR; $target=Join-Path $root 'Wildlight-Launcher.exe'; if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { $target=Join-Path $root 'runtime\wildlight-windows-arm64.exe' }; if (-not (Test-Path -LiteralPath $target)) { throw 'Extract the entire game ZIP before creating a shortcut.' }; $desktop=[Environment]::GetFolderPath('Desktop'); if (-not $desktop) { throw 'Your Desktop folder could not be found.' }; $shell=New-Object -ComObject WScript.Shell; $link=$shell.CreateShortcut((Join-Path $desktop 'Wildlight Frontier.lnk')); $link.TargetPath=$target; $link.WorkingDirectory=$root; $link.IconLocation=$target+',0'; $link.Description='Wildlight Frontier - offline exploration and two-player LAN co-op'; $link.Save(); Write-Host 'Wildlight Frontier shortcut created on your Desktop.'"
if errorlevel 1 (
 echo The shortcut could not be created. You can still launch Wildlight-Launcher.exe directly.
 pause
 exit /b 1
)
echo Keep the extracted game folder in this location. Recreate the shortcut after moving it.
pause
