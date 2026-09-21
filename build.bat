@echo off
setlocal

go version

set CGO_ENABLED=0
set GOARCH=amd64

set WIN_ANS=
set /p WIN_ANS=Build Windows (bot.exe)? [y/n]: 
if /i not "%WIN_ANS%"=="y" goto skip_windows

echo === plumebot windows build ===
set GOOS=windows
go build -trimpath -ldflags "-s -w" -o bot.exe ./cmd/bot/
if errorlevel 1 (
    echo [FAILED] build bot.exe
    exit /b 1
)
echo [OK] bot.exe
goto ask_linux

:skip_windows
echo [SKIP] bot.exe

:ask_linux
set LINUX_ANS=
set /p LINUX_ANS=Build Linux (bot-linux)? [y/n]: 
if /i not "%LINUX_ANS%"=="y" goto skip_linux

echo === plumebot linux build ===
set GOOS=linux
go build -trimpath -ldflags "-s -w" -o bot-linux ./cmd/bot/
if errorlevel 1 (
    echo [FAILED] build bot-linux
    exit /b 1
)
echo [OK] bot-linux
goto done

:skip_linux
echo [SKIP] bot-linux

:done
endlocal