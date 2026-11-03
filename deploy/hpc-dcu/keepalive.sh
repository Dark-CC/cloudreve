#!/bin/bash
# 装到登录节点 crontab：*/10 * * * * /public/home/acslbzzr2m/llm-service/keepalive.sh
# 平台无 systemd、作业墙钟上限 72 小时，用 cron 看护：作业不在队列里就重提。
# 只在登录节点跑 cron/提交，不在登录节点跑推理。
set -uo pipefail
. "$HOME/llm-service/env.sh"
mkdir -p "$SVC/logs"

if squeue -h -u "$USER" -o "%j" | grep -qx "$JOB_NAME"; then
    exit 0
fi
# 作业不在队列（到期被杀或失败）：清干净残留的节点握手文件再重提
: > "$CURRENT_NODE_FILE" 2>/dev/null || true
echo "[keepalive] $(date) 未发现 $JOB_NAME，重新提交" >> "$SVC/logs/keepalive.log"
sbatch "$SVC/serve.slurm" >> "$SVC/logs/keepalive.log" 2>&1
