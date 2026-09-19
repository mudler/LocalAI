#!/bin/bash
set -euo pipefail

BACKEND_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
# The package is generated; never mix libraries from different build variants.
rm -rf -- "$BACKEND_DIR/package"
mkdir -p "$BACKEND_DIR/package/lib"
if [ -d "$BACKEND_DIR/${BUILD_DIR:-build-cpu}-avx2" ]; then
  mkdir -p "$BACKEND_DIR/package/variants/avx2"
  find "$BACKEND_DIR/${BUILD_DIR:-build-cpu}-avx2" -name 'libggml-cpu.so*' -exec cp -a {} "$BACKEND_DIR/package/variants/avx2/" \;
fi
cp "$BACKEND_DIR/kimodocpp" "$BACKEND_DIR/run.sh" "$BACKEND_DIR/package/"
chmod +x "$BACKEND_DIR/package/run.sh"
find "$BACKEND_DIR/${BUILD_DIR:-build-cpu}" \( -name 'libkimodo.so*' -o -name 'libkimodo.dylib' -o -name 'libggml*.so*' -o -name 'libggml*.dylib' \) -exec cp -a {} "$BACKEND_DIR/package/lib/" \;
cp "$BACKEND_DIR/sources/kimodo.cpp/LICENSE" "$BACKEND_DIR/package/LICENSE.kimodo"
cp "$BACKEND_DIR/sources/kimodo.cpp/NOTICE" "$BACKEND_DIR/package/NOTICE.kimodo"
cp "$BACKEND_DIR/sources/kimodo.cpp/ggml/LICENSE" "$BACKEND_DIR/package/LICENSE.ggml"

if [ "$(uname -s)" != Darwin ]; then
  # purego's executable also imports libdl/libpthread, even with CGO disabled.
  for library in "$BACKEND_DIR/package/kimodocpp" "$BACKEND_DIR"/package/lib/*.so*; do
    while read -r dependency; do
      [ -f "$dependency" ] && cp -L "$dependency" "$BACKEND_DIR/package/lib/"
    done < <(ldd "$library" | awk '/=> \// {print $3}')
  done
  loader=$(ldd "$BACKEND_DIR/package/lib/libkimodo.so" | awk '/ld-linux/ {print $1; exit}')
  if [ -f "$loader" ]; then cp -L "$loader" "$BACKEND_DIR/package/lib/ld.so"; fi
  source "$BACKEND_DIR/../../../scripts/build/package-gpu-libs.sh" "$BACKEND_DIR/package/lib"
  package_gpu_libs
  if [ "${BUILD_TYPE:-}" = vulkan ]; then
    # NVIDIA's host ICD dlopens EGL; it is not visible in libkimodo's ldd tree.
    for soname in libEGL.so.1 libGLdispatch.so.0 libX11.so.6 libXext.so.6; do
      dependency=$(ldconfig -p | awk -v name="$soname" '$1 == name {print $NF; exit}')
      if [ ! -f "$dependency" ]; then
        echo "Missing Vulkan ICD runtime dependency: $soname" >&2
        exit 1
      fi
      copy_lib "$dependency"
    done
    sweep_transitive_deps
  fi
fi
