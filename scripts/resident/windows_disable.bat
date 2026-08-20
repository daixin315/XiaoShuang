@echo off
REM 小双常驻【关闭】（Windows）：移除启动文件夹里的自动启动脚本
setlocal
set "STARTUP=%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup"
del /Q "%STARTUP%\fish_desktop.bat" 2>nul

echo 小双常驻已关闭
echo 手动启动方式：双击 fish_desktop.exe
pause
