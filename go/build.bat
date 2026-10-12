@echo off
rem Rebuilds dist\MicroscopeViewer.exe (pure Go, no cgo). Uses the local Go in ..\tools\go if present.
cd /d "%~dp0"
set "GO=go"
if exist "..\tools\go\bin\go.exe" set "GO=..\tools\go\bin\go.exe"
"%GO%" build -trimpath -ldflags "-H windowsgui -s -w" -o ..\dist\MicroscopeViewer.exe . || exit /b 1
echo Built ..\dist\MicroscopeViewer.exe
