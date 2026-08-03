@echo off
rem 单独启动双模型服务, 并按 config.env 设置端口等参数。
rem
rem 用途: 需要把模型服务和主服务分开管理 (例如分别放在两个窗口观察日志) 时使用。
rem 直接双击 noblack-model.exe 会绕过 config.env, 端口等配置不生效, 请改用本脚本。
rem
rem 常规使用请用 start.cmd, 它会依次启动模型服务和主服务。
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0noblack-control.ps1" -Action StartModelOnly
exit /b %errorlevel%
