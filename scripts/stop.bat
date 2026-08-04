@echo off
rem 停止 tmm：向本地服务发送关闭指令（优雅停机），随后退出。
rem 若服务未启动，脚本无副作用，可直接退出。
curl -s -X POST http://127.0.0.1:8080/api/shutdown >nul 2>&1
echo 已发送停止指令。
timeout /t 2 >nul 2>&1
exit
