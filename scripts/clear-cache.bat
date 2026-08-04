@echo off
rem 清除 tmm 的本地缓存（media/meta/preview 内容），保留 cache 目录结构。
rem
rem 便携版目录布局：
rem   tmm.exe
rem   data\        <- 账号登录会话、peers、任务（本脚本【不】动它）
rem   cache\       <- media/meta/preview 缓存（本脚本清空其内容，保留目录）
rem   downloads\   <- 下载的文件（本脚本【不】动它）
rem   logs\        <- 运行日志（本脚本【不】动它）
rem
rem 用法：先 stop.bat 停止服务，再运行本脚本，最后 start.bat 启动。
cd /d "%~dp0"
if exist cache (
  rem 删除 cache 下的所有子目录（media/meta/preview 等）
  for /d %%D in (cache\*) do rmdir /s /q "%%D"
  rem 删除 cache 下的残留文件（如果有）
  del /q /f cache\* 2>nul
  echo 已清空 cache\ 内容（media, meta, preview），目录已保留
) else (
  echo cache\ 不存在，无需清除
)
echo 账号数据 data\ 未受影响，无需重新登录。
echo 完成。现在可以 start.bat 启动做干净测试。
