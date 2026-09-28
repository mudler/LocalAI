#!/bin/bash
set -euo pipefail
CURDIR=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$CURDIR/package/lib"
cp "$CURDIR/sources/pixal3d/LICENSE" "$CURDIR/package/LICENSE.pixal3d"
cp "$CURDIR/sources/pixal3d/thirdparty/ggml/LICENSE" "$CURDIR/package/LICENSE.ggml"
cp "$CURDIR/pixal3dcpp" "$CURDIR/native/trellis-cli" "$CURDIR/run.sh" "$CURDIR/package/"
find "$CURDIR/native" \( -name 'libggml*.so*' -o -name 'libggml*.dylib' \) -exec cp -a {} "$CURDIR/package/lib/" \;
if [ "$(uname -s)" = Darwin ]; then
    # Upstream uses Linux $ORIGIN rpaths; run.sh also exports DYLD_LIBRARY_PATH.
    install_name_tool -add_rpath '@executable_path/lib' "$CURDIR/package/trellis-cli"
else
    # Include the CLI's transitive runtime dependencies. Driver libraries stay
    # host-provided; package-gpu-libs owns the accelerator runtime inventory.
    ldd "$CURDIR/native/trellis-cli" | awk '/=> \/|^[[:space:]]*\// {for (i=1;i<=NF;i++) if ($i ~ /^\//) print $i}' | while read -r library; do
        case "$library" in
            */libcuda.so*|*/libnvidia-*) continue ;;
        esac
        cp -Lf "$library" "$CURDIR/package/lib/"
    done
    for loader in /lib64/ld-linux-x86-64.so.2 /lib/ld-linux-aarch64.so.1; do
        if [ -f "$loader" ]; then cp -Lf "$loader" "$CURDIR/package/lib/ld.so"; break; fi
    done
fi
source "$CURDIR/../../../scripts/build/package-gpu-libs.sh" "$CURDIR/package/lib"
package_gpu_libs
