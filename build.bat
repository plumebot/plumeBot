@echo off
setlocal

go version

echo === plumebot windows build ===
go build -trimpath -ldflags "-s -w" -o bot.exe ./cmd/bot/
if errorlevel 1 (
    echo [FAILED] build bot.exe
    exit /b 1
)

echo === plumebot linux build ===
set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0

go build -trimpath -ldflags "-s -w" -o bot-linux ./cmd/bot/
if errorlevel 1 (
    echo [FAILED] build bot-linux
    exit /b 1
)


echo [OK] bot-linux
endlocal