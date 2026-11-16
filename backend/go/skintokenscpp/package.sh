#!/bin/bash
set -euo pipefail

BACKEND_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
# The package is generated; never mix libraries from different build variants.
rm -rf -- "$BACKEND_DIR/package"
mkdir -p "$BACKEND_DIR/package/lib"
cp "$BACKEND_DIR/skintokenscpp" "$BACKEND_DIR/run.sh" "$BACKEND_DIR/package/"
chmod +x "$BACKEND_DIR/package/run.sh"
find "$BACKEND_DIR/${BUILD_DIR:-build-cpu}" \( -name 'libskintokens.so*' -o -name 'libskintokens.dylib' -o -name 'libggml*.so*' -o -name 'libggml*.dylib' \) -exec cp -a {} "$BACKEND_DIR/package/lib/" \;
cp "$BACKEND_DIR/sources/skin-tokens.cpp/LICENSE" "$BACKEND_DIR/package/LICENSE.skintokens"
cp "$BACKEND_DIR/sources/skin-tokens.cpp/NOTICE" "$BACKEND_DIR/package/NOTICE.skintokens"
cp "$BACKEND_DIR/sources/skin-tokens.cpp/ggml/LICENSE" "$BACKEND_DIR/package/LICENSE.ggml"

if [ "$(uname -s)" != Darwin ]; then
  # purego's executable also imports libdl/libpthread, even with CGO disabled.
  for library in "$BACKEND_DIR/package/skintokenscpp" "$BACKEND_DIR"/package/lib/*.so*; do
    while read -r dependency; do
      [ -f "$dependency" ] && cp -fL "$dependency" "$BACKEND_DIR/package/lib/"
    done < <(ldd "$library" | awk '/=> \// {print $3}')
  done
  loader=$(ldd "$BACKEND_DIR/package/lib/libskintokens.so" | awk '/ld-linux/ {print $1; exit}')
  if [ -f "$loader" ]; then cp -L "$loader" "$BACKEND_DIR/package/lib/ld.so"; fi
  source "$BACKEND_DIR/../../../scripts/build/package-gpu-libs.sh" "$BACKEND_DIR/package/lib"
  package_gpu_libs
  if [ "${BUILD_TYPE:-}" = vulkan ]; then
    # NVIDIA's host ICD dlopens EGL; it is not visible in libskintokens's ldd tree.
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
else
  # Homebrew's OpenMP runtime is not a macOS system library. Bundle and rebase
  # it so an installed backend does not require the build machine's Cellar.
  omp_prefix=$(brew --prefix libomp)
  cp -L "$omp_prefix/lib/libomp.dylib" "$BACKEND_DIR/package/lib/"
  for library in "$BACKEND_DIR"/package/lib/*.dylib; do
    chmod u+w "$library"
    install_name_tool -id "@rpath/$(basename "$library")" "$library"
    while read -r dependency; do
      case "$dependency" in
        /opt/homebrew/*|/usr/local/*)
          bundled="$BACKEND_DIR/package/lib/$(basename "$dependency")"
          if [ ! -f "$bundled" ]; then echo "Unbundled runtime dependency: $dependency" >&2; exit 1; fi
          install_name_tool -change "$dependency" "@loader_path/$(basename "$dependency")" "$library"
          ;;
      esac
    done < <(otool -L "$library" | tail -n +2 | awk '{print $1}')
    codesign --force --sign - "$library"
  done
fi
