@echo off
setlocal
cd /d "%~dp0"
if not exist "app\index.html" (
  echo Extract the entire ZIP first, then launch again.
  pause
  exit /b 1
)
if /i "%PROCESSOR_ARCHITECTURE%"=="ARM64" (
  "runtime\wildlight-windows-arm64.exe" %*
) else (
  "Wildlight-Launcher.exe" %*
)
if errorlevel 1 pause
