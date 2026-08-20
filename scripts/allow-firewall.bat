@echo off
net session >nul 2>&1
if %errorlevel% neq 0 (
  echo Please run this script as Administrator.
  pause
  exit /b 1
)

netsh advfirewall firewall add rule name="LAN Share TCP 51888" dir=in action=allow protocol=TCP localport=51888 profile=private
netsh advfirewall firewall add rule name="LAN Share UDP 51889" dir=in action=allow protocol=UDP localport=51889 profile=private

echo.
echo LAN Share firewall rules added.
pause
