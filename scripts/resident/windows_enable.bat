@echo off
REM 小双常驻【开启】（Windows）：绿色版启动脚本放入启动文件夹，登录自动运行
setlocal
set "APP_DIR=%~dp0..\.."
set "STARTUP=%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup"

if not exist "%APP_DIR%\fish_desktop.exe" (
    echo [错误] 未找到 fish_desktop.exe，请把本脚本放在绿色版目录的 scripts\resident\ 下
    pause
    exit /b 1
)

> "%STARTUP%\fish_desktop.bat" echo @echo off
>> "%STARTUP%\fish_desktop.bat" echo start "" "%APP_DIR%\fish_desktop.exe"

echo.
echo 小双常驻已开启：登录 Windows 后自动启动
echo 取消常驻请运行 windows_disable.bat
pause
