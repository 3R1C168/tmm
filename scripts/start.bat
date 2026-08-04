@echo off
rem 启动 tmm：双击本脚本，浏览器打开 http://127.0.0.1:8080
rem 关闭本窗口（或 Ctrl+C）即停止服务。
rem 日志输出到此窗口，同时写入 logs\server.log。
cd /d "%~dp0"
"%~dp0tmm.exe" serve --port 8080
