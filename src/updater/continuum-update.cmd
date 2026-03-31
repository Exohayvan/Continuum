@echo off
setlocal
set "CURRENT=C:\Apps\Continuum.exe"
set "REPLACEMENT=C:\Temp\Continuum.exe"
set "BACKUP=C:\Apps\Continuum.exe.bak"
for /L %%i in (1,1,30) do (
  move /Y "%CURRENT%" "%BACKUP%" >nul 2>nul && goto replaced
  ping 127.0.0.1 -n 2 >nul
)
exit /b 1
:replaced
move /Y "%REPLACEMENT%" "%CURRENT%" >nul 2>nul
start "" "%CURRENT%"
del "%~f0"