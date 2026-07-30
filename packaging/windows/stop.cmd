@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0noblack-control.ps1" -Action Stop
exit /b %errorlevel%
