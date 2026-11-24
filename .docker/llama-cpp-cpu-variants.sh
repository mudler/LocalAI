#!/usr/bin/env bash
# Build ggml's CPU backend variants (libggml-cpu-*.so) on their own, with plain gcc.
#
# The variants do not depend on the GPU backend: each one is a dlopen-able module
# that links only libggml-base. So they are built once per architecture here, in
# a stage on a plain toolchain, and copied into every llama-cpp image. The GPU
# image then compiles only its own backend and the loader, with the CPU backend
# switched off (-DGGML_CPU=OFF), so icpx and hipcc never see ggml-cpu sources.
#
# Usage: llama-cpp-cpu-variants.sh <Makefile with the LLAMA_VERSION pin> <output dir>
set -euxo pipefail

makefile=${1:?Makefile that carries the LLAMA_VERSION pin is required}
out=${2:?output directory is required}

version=$(sed -n 's/^LLAMA_VERSION?=//p' "$makefile" | head -n 1)
repo=$(sed -n 's/^LLAMA_REPO?=//p' "$makefile" | head -n 1)
[ -n "$version" ] && [ -n "$repo" ] || { echo "no LLAMA_VERSION or LLAMA_REPO in $makefile" >&2; exit 1; }

src=${CPU_VARIANTS_SRC:-/build/llama.cpp}
mkdir -p "$src" "$out"
cd "$src"
git init -q
git remote add origin "$repo"
attempt=1
until git fetch -q --depth 1 origin "$version"; do
  [ "$attempt" -lt 3 ] || { echo "git fetch failed after $attempt attempts" >&2; exit 1; }
  sleep $((attempt * 3))
  attempt=$((attempt + 1))
done
git checkout -q FETCH_HEAD

# Same flags the single-tree build used: shared ggml, backends as modules, every
# variant of the table, no host-specific tuning.
cmake -S . -B build \
  -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_SHARED_LIBS=ON \
  -DGGML_BACKEND_DL=ON \
  -DGGML_CPU_ALL_VARIANTS=ON \
  -DGGML_NATIVE=OFF \
  -DCMAKE_SKIP_BUILD_RPATH=ON \
  -DLLAMA_CURL=OFF \
  -DLLAMA_OPENSSL=OFF \
  -DCMAKE_C_COMPILER_LAUNCHER=ccache \
  -DCMAKE_CXX_COMPILER_LAUNCHER=ccache
cmake --build build --config Release -j "$(nproc)" --target ggml

cp -av build/bin/libggml-cpu-*.so "$out"/
echo "$version" > "$out/LLAMA_VERSION"

# The package runs on the loader and libc bundled from the GPU builder image (for
# example Ubuntu 22.04 on JetPack), which can be older than this stage. A variant that
# needs a newer glibc or libstdc++ would fail to load there, so refuse to ship one.
# The limits are those of Ubuntu 22.04.
check_max() { # <symbol prefix> <limit> <file>
  local need
  need=$(objdump -T "$3" | grep -o "$1_[0-9.]*" | sed "s/^$1_//" | sort -uV | tail -n 1)
  if [ -n "$need" ] && [ "$(printf '%s\n%s\n' "$need" "$2" | sort -V | tail -n 1)" != "$2" ]; then
    echo "$3 needs $1 $need, newer than the allowed $2" >&2
    exit 1
  fi
}
for f in "$out"/libggml-cpu-*.so; do
  check_max GLIBC "${CPU_VARIANTS_MAX_GLIBC:-2.35}" "$f"
  check_max GLIBCXX "${CPU_VARIANTS_MAX_GLIBCXX:-3.4.30}" "$f"
  check_max CXXABI "${CPU_VARIANTS_MAX_CXXABI:-1.3.13}" "$f"
done
ls -la "$out"
