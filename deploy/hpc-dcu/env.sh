# shellcheck shell=bash
# 统一环境变量：集群侧（登录节点与计算节点）所有脚本 source 本文件。
# 注意：不要 load compiler/dtk/*，计算节点已默认加载 compiler/rocm/dtk-22.10.1，
# 再 load 会报 "conflicts with the currently loaded module(s)"（实测）。

SVC=${SVC:-$HOME/llm-service}
SRC=$SVC/src/llama.cpp
MODELS=$SVC/models
BUILD=$SRC/build-hip
PORT=${PORT:-18180}
APIKEY_FILE=$SVC/apikey
CURRENT_NODE_FILE=$SVC/current-node.txt
GGUF=$MODELS/qwen2.5-32b-instruct-q4_k_m-00001-of-00005.gguf
JOB_NAME=llm-serve
PARTITION=kshdtest

# 32B 单卡放不下，构建期冒烟测试亦须两卡
GRES=dcu:2

export SVC SRC MODELS BUILD PORT APIKEY_FILE CURRENT_NODE_FILE GGUF JOB_NAME PARTITION GRES

load_modules() {
    module load compiler/cmake/3.25.0 >/dev/null 2>&1 || true
    module load apps/python/3.8.13 >/dev/null 2>&1 || true
}
