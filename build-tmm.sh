#!/usr/bin/env bash
# build-tmm.sh — 构建带版本号的 tmm.exe 并部署到 tmm 目录
#
# 用法（在 tdl 目录下）：
#   ./build-tmm.sh           # 只构建后端 exe
#   ./build-tmm.sh web       # 同时构建前端并部署
#
# 版本号来自 git describe（如 v0.20.3-85-gabcdef1），注入到 consts.Version。
# 工作区有未提交改动时版本号会带 -dirty 后缀，提醒你「这个 exe 含未提交代码」。
set -euo pipefail

cd "$(dirname "$0")"

# --- 版本号（git describe）---
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "unknown")
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE=$(git log -1 --format=%cd --date=format:%Y-%m-%dT%H:%M:%S 2>/dev/null || echo "unknown")
echo "==> 版本: $VERSION (commit $DATE)"

# --- 清缓存 + 构建后端 ---
echo "==> go clean -cache（避免缓存命中旧产物）"
go clean -cache
echo "==> go build（注入版本号）"
go build -ldflags="-s -w \
  -X github.com/iyear/tdl/pkg/consts.Version=$VERSION \
  -X github.com/iyear/tdl/pkg/consts.Commit=$COMMIT \
  -X github.com/iyear/tdl/pkg/consts.CommitDate=$DATE" \
  -o ../tmm/tmm.exe .

echo "==> 后端构建完成: ../tmm/tmm.exe ($VERSION)"

# --- 可选：前端构建 + 部署 ---
if [[ "${1:-}" == "web" ]]; then
  echo "==> 构建前端"
  (cd web/app && npm run build)
  echo "==> 部署前端到 tmm/web"
  rm -rf ../tmm/web/assets
  cp -r web/app/dist/assets ../tmm/web/assets
  cp web/app/dist/index.html ../tmm/web/index.html
  echo "==> 前端部署完成"
fi

echo "==> 全部完成。验证版本: ../tmm/tmm.exe version"
