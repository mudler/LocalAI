#!/bin/bash
set -euo pipefail
BACKEND_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
rm -rf -- "$BACKEND_DIR/package"
mkdir -p "$BACKEND_DIR/package/lib" "$BACKEND_DIR/package/source"
cp "$BACKEND_DIR/meshremesh" "$BACKEND_DIR/run.sh" "$BACKEND_DIR/README.md" "$BACKEND_DIR/NOTICE" "$BACKEND_DIR/package/"
chmod +x "$BACKEND_DIR/package/run.sh"
find "$BACKEND_DIR/build-native" \( -name 'libmeshremesh.so*' -o -name 'libmeshremesh.dylib' \) -exec cp -a {} "$BACKEND_DIR/package/lib/" \;
cp -R "$BACKEND_DIR/native" "$BACKEND_DIR/package/source/"
cp "$BACKEND_DIR"/*.go "$BACKEND_DIR/CMakeLists.txt" "$BACKEND_DIR/Makefile" "$BACKEND_DIR/package/source/"
cp "$BACKEND_DIR/sources/eigen/COPYING.MPL2" "$BACKEND_DIR/package/LICENSE.Eigen"
if [ -f /usr/share/doc/libcgal-dev/copyright ]; then
  cp /usr/share/doc/libcgal-dev/copyright "$BACKEND_DIR/package/LICENSE.CGAL"
fi
if [ "$(uname -s)" != Darwin ]; then
  for library in "$BACKEND_DIR/package/meshremesh" "$BACKEND_DIR"/package/lib/*.so*; do
    while read -r dependency; do
      [ -f "$dependency" ] && cp -fL "$dependency" "$BACKEND_DIR/package/lib/"
    done < <(ldd "$library" | awk '/=> \// {print $3}')
  done
  loader=$(ldd "$BACKEND_DIR/package/lib/libmeshremesh.so" | awk '/ld-linux/ {print $1; exit}')
  if [ -f "$loader" ]; then cp -L "$loader" "$BACKEND_DIR/package/lib/ld.so"; fi
else
  # CGAL is header-only, but its GMP/MPFR dependencies are not system dylibs.
  for dependency in gmp mpfr; do
    prefix=$(brew --prefix "$dependency")
    for library in "$prefix"/lib/*.dylib; do cp -L "$library" "$BACKEND_DIR/package/lib/"; done
  done
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
