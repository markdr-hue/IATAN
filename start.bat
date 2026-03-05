@echo off
echo Starting IATAN_GO...
echo Admin panel: http://localhost:5001
echo Public site:  http://localhost:5000
echo.

if not exist "iatan.exe" (
    echo Building...
    go build -o iatan.exe .
    if errorlevel 1 (
        echo Build failed!
        pause
        exit /b 1
    )
)

iatan.exe
pause
