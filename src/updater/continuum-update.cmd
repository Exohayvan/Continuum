@echo off
setlocal
set "CURRENT=C:\Apps\Continuum.exe"
set "REPLACEMENT=C:\Temp\Continuum.exe"
set "PREVIOUS=C:\Apps\Continuum.exe.previous"
for /L %%i in (1,1,30) do (
  move /Y "%CURRENT%" "%PREVIOUS%" >nul 2>nul && goto replaced
  ping 127.0.0.1 -n 2 >nul
)
exit /b 1
:replaced
move /Y "%REPLACEMENT%" "%CURRENT%" >nul 2>nul || goto restore
start "" "%CURRENT%"
del /Q "%PREVIOUS%" >nul 2>nul
del "%~f0"
exit /b 0
:restore
move /Y "%PREVIOUS%" "%CURRENT%" >nul 2>nul
exit /b 1