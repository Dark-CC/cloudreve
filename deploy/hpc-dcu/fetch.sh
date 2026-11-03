#!/bin/bash
# 在登录节点执行：拉取 llama.cpp 源码（经 pypi sdist，GitHub 不可达）与 GGUF 模型分片。
# 可重复执行：源码解包幂等，模型用 curl -C - 续传。
set -euo pipefail
. "$HOME/llm-service/env.sh"

TMP=$(mktemp -d "$HOME/.tmp-fetch.XXXXXX")
trap 'rm -rf "$TMP"' EXIT
cd "$TMP"

echo "== [1/2] 源码 llama.cpp (via pypi sdist) =="
if [ -f "$SRC/CMakeLists.txt" ]; then
    echo "already present: $SRC"
else
    python3 -m pip download llama-cpp-python==0.3.36 --no-deps --no-binary :all: \
        -d "$TMP" -i https://pypi.org/simple 2>&1 | tail -5
    Sdist=$(ls "$TMP"/llama_cpp_python-*.tar.gz | head -1)
    echo "sdist: $Sdist"
    tar xzf "$Sdist" -C "$TMP"
    mkdir -p "$(dirname "$SRC")"
    rm -rf "$SRC"
    mv "$TMP"/llama_cpp_python-*/vendor/llama.cpp "$SRC"
    echo "extracted: $(find "$SRC" -type f | wc -l) files"
fi
test -d "$SRC/tools/server" || { echo "FATAL: 源码缺 tools/server"; exit 1; }

echo "== [2/2] 模型 Qwen2.5-32B-Instruct Q4_K_M (ModelScope, 5 片 18.49GB) =="
mkdir -p "$MODELS"
BASE=https://modelscope.cn/models/Qwen/Qwen2.5-32B-Instruct-GGUF/resolve/master
for i in 1 2 3 4 5; do
    F=qwen2.5-32b-instruct-q4_k_m-0000${i}-of-00005.gguf
    [ -s "$MODELS/$F" ] && [ ! -f "$MODELS/$F.part" ] && { echo "skip $F"; continue; }
    echo "-- $F"
    curl -fL -C - --retry 3 --retry-delay 5 -o "$MODELS/$F" "$BASE/$F"
done

echo "== sizes =="
ls -l "$MODELS"/*.gguf | awk '{printf "%-52s %8.2f GB\n", $9, $5/1073741824}'
du -sh "$MODELS"
