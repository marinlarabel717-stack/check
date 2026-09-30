@echo off
setlocal
cd /d "%~dp0"
powershell -STA -ExecutionPolicy Bypass -File "%~dp0start.ps1"
