#!/bin/bash
#SBATCH -J llm-build
#SBATCH -p kshdtest
#SBATCH -N 1
#SBATCH -n 1
#SBATCH --gres=dcu:2
#SBATCH -t 01:00:00
# 编译 llama.cpp 的 HIP 后端，并用真实 32B 模型做两卡冒烟测试。
# 32B 单卡装不下，冒烟测试必须两卡（与 serve.slurm 同形状）。
set -euo pipefail
. "$HOME/llm-service/env.sh"
mkdir -p "$SVC/logs"

echo "== node: $(hostname) job: ${SLURM_JOB_ID:-manual} date: $(date) =="
module list 2>&1 | tail -5
load_modules
cmake --version | head -1
which hipcc || true
echo "ROCR_VISIBLE_DEVICES=${ROCR_VISIBLE_DEVICES:-unset}"
rocm-smi --showmeminfo vram 2>&1 | grep -iE "vram" | head -10 || true

rm -rf "$BUILD"
echo "== configure =="
cmake -S "$SRC" -B "$BUILD" \
    -DGGML_HIP=ON -DAMDGPU_TARGETS=gfx906 \
    -DCMAKE_BUILD_TYPE=Release \
    -DLLAMA_CURL=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF

echo "== build =="
cmake --build "$BUILD" --target llama-server llama-cli -j 16

ls -l "$BUILD/bin/"

echo "== smoke test: 两卡推理真实 32B 模型 =="
"$BUILD/bin/llama-cli" -m "$GGUF" -ngl 999 -sm layer -c 2048 -no-cnv \
    -p "你好，请用一句话介绍你自己。" -n 32 --threads 16 2>&1 | tail -25

echo "== vram after smoke test =="
rocm-smi --showmeminfo vram 2>&1 | grep -iE "vram" | head -10
