@echo off
echo Building IATAN...
go build -o iatan.exe . || (
    echo.
    echo Build failed! Press any key to close.
    pause >nul
    exit /b 1
)
echo.
echo Starting IATAN...
echo Admin panel: http://localhost:5001
echo Public site:  http://localhost:5000
echo.
iatan.exe
echo.
echo IATAN has stopped. Press any key to close.
pause >nul
