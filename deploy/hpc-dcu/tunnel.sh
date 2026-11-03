#!/bin/bash
# Baixiong 侧常驻隧道：把公网 0.0.0.0:18180 转发到集群当前计算节点的 18180。
# 由 systemd 单元 model-gateway.service 拉起，Restart=always 兜底。
set -uo pipefail

KEY=/root/.ssh/model_server
APIKEY=/opt/model-gateway/apikey
CUR=/opt/model-gateway/current-node.cache
REMOTE=acslbzzr2m@58.240.173.20
SSH_PORT=65023
LOCAL_PORT=18180
LOG=/var/log/model-gateway.log

log() { echo "[$(date '+%F %T')] $*" >> "$LOG"; }

log "tunnel 启动"
while true; do
    NODE=$(ssh -4 -p "$SSH_PORT" -i "$KEY" \
        -o BatchMode=yes -o StrictHostKeyChecking=accept-new \
        -o ConnectTimeout=20 -o ServerAliveInterval=30 \
        "$REMOTE" 'cat ~/llm-service/current-node.txt 2>/dev/null' 2>>"$LOG" | tr -d '\r\n')

    if [ -z "$NODE" ]; then
        log "current-node.txt 为空（服务作业未在跑），30 秒后重试"
        sleep 30
        continue
    fi
    echo "$NODE" > "$CUR"
    log "当前计算节点: $NODE，建立 -L 0.0.0.0:$LOCAL_PORT:$NODE:18180"

    ssh -4 -N -p "$SSH_PORT" -i "$KEY" \
        -o BatchMode=yes -o StrictHostKeyChecking=accept-new \
        -o ExitOnForwardFailure=yes \
        -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
        -o ConnectTimeout=20 \
        -L "0.0.0.0:$LOCAL_PORT:$NODE:18180" \
        "$REMOTE" 2>>"$LOG"

    log "转发断开（exit=$?），15 秒后重建"
    sleep 15
done
