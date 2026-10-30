#!/bin/bash
# 在 centos-stream-server 上部署 Cloudreve fork 集成测试站（端口 15220）。
#
# 用法: ./deploy.sh [源码目录] [二进制路径]
#   默认: ./deploy.sh /data/cr-src /data/cr-build/cloudreve
#
# 完整构建顺序（缺一步都会失败，原因见 docs/build-log-1030.md）:
#   1) 前端打包 → 产物落在 <repo>/assets/build/（vite outDir 相对仓库根）
#   2) 打成 application/statics/assets.zip（go:embed 必须能找到它）
#   3) go build 后端
#   4) 跑本脚本部署
set -euo pipefail

SRC="${1:-/data/cr-src}"
BIN="${2:-/data/cr-build/cloudreve}"
ROOT=/data/cr-test-15220
PORT=15220
SVC=cloudreve-test-15220

[ -x "$BIN" ] || { echo "二进制不存在或不可执行: $BIN" >&2; exit 1; }
[ -f "$SRC/application/statics/assets.zip" ] || {
  echo "缺少 $SRC/application/statics/assets.zip —— 先跑前端打包并生成该 zip" >&2
  exit 1
}

echo "==> 目录 $ROOT"
mkdir -p "$ROOT/data/log" "$ROOT/data/temp/aria2"

echo "==> 备份当前二进制（回滚用）"
if [ -f "$ROOT/cloudreve" ]; then
  cp -a "$ROOT/cloudreve" "$ROOT/cloudreve.rollback-$(date +%Y%m%d-%H%M%S)"
  ls -1t "$ROOT"/cloudreve.rollback-* 2>/dev/null | tail -n +3 | xargs -r rm -f
fi

echo "==> 安装二进制"
install -m 0755 "$BIN" "$ROOT/cloudreve"

echo "==> SELinux 标记（Enforcing 下必需，否则 203/EXEC）"
if command -v getenforce >/dev/null && [ "$(getenforce)" = "Enforcing" ]; then
  semanage fcontext -a -t bin_t "$ROOT/cloudreve" 2>/dev/null || semanage fcontext -m -t bin_t "$ROOT/cloudreve"
  restorecon -v "$ROOT/cloudreve"
fi

echo "==> systemd 单元"
cat >/etc/systemd/system/${SVC}.service <<UNIT
[Unit]
Description=Cloudreve fork integration test site (port ${PORT})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${ROOT}
Environment=CR_CONF_System__Mode=master
Environment=CR_CONF_System__Listen=0.0.0.0:${PORT}
Environment=CR_CONF_Database__Type=sqlite3
Environment=CR_CONF_Database__DBFile=${ROOT}/data/cloudreve.db
ExecStart=${ROOT}/cloudreve
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable ${SVC}.service

echo "==> 防火墙放行 ${PORT}/tcp"
if systemctl is-active --quiet firewalld; then
  firewall-cmd --permanent --add-port=${PORT}/tcp >/dev/null
  firewall-cmd --reload >/dev/null
  echo "    ports: $(firewall-cmd --list-ports)"
fi

echo "==> 启动"
systemctl restart ${SVC}.service
sleep 5
systemctl is-active ${SVC}.service
ss -lntp | grep ":${PORT}" || true

echo "==> 内网可达性"
for ip in $(ip -4 addr show | awk '/inet / && !/127\.0\.0\.1/ {print $2}' | cut -d/ -f1); do
  curl -I -s -o /dev/null -w "    http://${ip}:${PORT}/ -> %{http_code}\n" "http://${ip}:${PORT}/" || echo "    http://${ip}:${PORT}/ -> 不可达"
done
