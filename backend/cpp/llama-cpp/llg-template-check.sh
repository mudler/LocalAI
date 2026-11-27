#!/bin/bash
#
# Builds llama.cpp at the pinned commit with LocalAI's patches and llguidance
# (CPU only, no gRPC), then runs llg_template_grammars_check over every chat
# template of the tree (see PATCHES.md, rules for 0008). CI runs it on pull
# requests that touch backend/cpp/llama-cpp, so a llama.cpp bump that breaks
# the translation of the template grammars fails there.
#
# Needs git, cmake, a C++ compiler, patch and cargo (llama.cpp builds
# llguidance with cargo).
#
# Env:
#   LLAMA_VERSION  llama.cpp commit (default: LLAMA_VERSION of the Makefile)
#   LLAMA_REPO     llama.cpp remote (default: https://github.com/ggml-org/llama.cpp)
#   WORK           work directory (default: ./llg-check next to this script)
#   JOBS           build jobs (default: nproc)
#   LLGUIDANCE_PREBUILT  directory with libllguidance.a and llguidance.h, used
#                  instead of building llguidance with cargo
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
LLAMA_VERSION="${LLAMA_VERSION:-$(sed -n 's/^LLAMA_VERSION?=//p' "$HERE/Makefile")}"
LLAMA_REPO="${LLAMA_REPO:-https://github.com/ggml-org/llama.cpp}"
WORK="${WORK:-$HERE/llg-check}"
JOBS="${JOBS:-$(nproc)}"

mkdir -p "$WORK"
cd "$WORK"

if [ ! -d llama.cpp/.git ]; then
    git init -q llama.cpp
    git -C llama.cpp remote add origin "$LLAMA_REPO"
fi
git -C llama.cpp fetch -q --depth 1 origin "$LLAMA_VERSION"
git -C llama.cpp checkout -q --force FETCH_HEAD
git -C llama.cpp clean -fdq
for p in "$HERE"/patches/*.patch; do
    echo "Applying patch $(basename "$p")"
    patch -s -d llama.cpp -p1 < "$p"
done

# llama.cpp as a subproject, plus the check linked to llama-common
cat > CMakeLists.txt <<EOF
cmake_minimum_required(VERSION 3.14)
project(llg_template_check C CXX)
add_subdirectory(llama.cpp)
add_executable(llg_template_grammars_check "$HERE/llg_template_grammars_check.cpp")
target_link_libraries(llg_template_grammars_check PRIVATE llama-common llama \${LLG_EXTRA_LIBS})
target_include_directories(llg_template_grammars_check PRIVATE llama.cpp/common llama.cpp/vendor)
target_compile_features(llg_template_grammars_check PRIVATE cxx_std_17)
EOF

llg_args=(-DLLAMA_LLGUIDANCE=ON)
if [ -n "${LLGUIDANCE_PREBUILT:-}" ]; then
    llg_args=(-DLLAMA_LLGUIDANCE=OFF
              "-DCMAKE_CXX_FLAGS=-DLLAMA_USE_LLGUIDANCE -I$LLGUIDANCE_PREBUILT"
              "-DLLG_EXTRA_LIBS=$LLGUIDANCE_PREBUILT/libllguidance.a;dl;pthread")
fi

cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF \
    -DLLAMA_BUILD_COMMON=ON -DLLAMA_CURL=OFF -DLLAMA_OPENSSL=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF \
    -DLLAMA_BUILD_TOOLS=OFF -DLLAMA_BUILD_SERVER=OFF "${llg_args[@]}"
cmake --build build -j "$JOBS" --target llg_template_grammars_check

M=llama.cpp/models
build/llg_template_grammars_check "$M/templates" \
    "$M/ggml-vocab-qwen35.gguf" "$M/ggml-vocab-llama-bpe.gguf" "$M/ggml-vocab-gemma-4.gguf"
