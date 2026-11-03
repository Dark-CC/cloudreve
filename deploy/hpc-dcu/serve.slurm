#!/bin/bash
#SBATCH -J llm-serve
#SBATCH -p kshdtest
#SBATCH -N 1
#SBATCH -n 1
#SBATCH --gres=dcu:2
#SBATCH -t 72:00:00
#SBATCH -o /public/home/acslbzzr2m/llm-service/logs/serve-%j.log
# 常驻模型服务：Qwen2.5-32B-Instruct Q4_K_M 按层切分到 2 张 DCU，
# 暴露 OpenAI 兼容 /v1/chat/completions，Bearer 鉴权。
set -uo pipefail
. "$HOME/llm-service/env.sh"

mkdir -p "$SVC/logs"
cleanup() { : > "$CURRENT_NODE_FILE"; echo "[serve] cleanup at $(date)"; }
trap cleanup EXIT

. "$HOME/llm-service/env.sh"
echo "== node: $(hostname) job: ${SLURM_JOB_ID:-manual} date: $(date) =="
echo "ROCR_VISIBLE_DEVICES=${ROCR_VISIBLE_DEVICES:-unset}"
rocm-smi --showmeminfo vram 2>&1 | grep -E "vram total|vram used" | head -8 || true

test -f "$APIKEY_FILE" || { echo "FATAL: 缺 $APIKEY_FILE"; exit 1; }
test -f "$GGUF" || { echo "FATAL: 缺模型 $GGUF"; exit 1; }

hostname > "$CURRENT_NODE_FILE"
echo "[serve] current-node = $(cat "$CURRENT_NODE_FILE")"

exec "$BUILD/bin/llama-server" \
    -m "$GGUF" \
    -ngl 999 \
    -sm layer \
    -c 16384 \
    --host 0.0.0.0 \
    --port "$PORT" \
    --api-key "$(cat "$APIKEY_FILE")" \
    --jinja \
    --threads 16 \
    --no-webui
