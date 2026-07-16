#!/bin/bash
# Assemble package/ for the from-scratch backend image: the grpc-server binary,
# run.sh, the dynamic loader, and every shared library the binary needs.
set -e
CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."

mkdir -p "$CURDIR/package/lib"
cp -avf "$CURDIR/grpc-server" "$CURDIR/package/"
cp -rfv "$CURDIR/run.sh"      "$CURDIR/package/"

# The dynamic loader, renamed to lib/ld.so so run.sh can invoke it explicitly
# (makes the image independent of the host's glibc layout).
if [ -f "/lib64/ld-linux-x86-64.so.2" ]; then
    cp -arfLv /lib64/ld-linux-x86-64.so.2 "$CURDIR/package/lib/ld.so"
elif [ -f "/lib/ld-linux-aarch64.so.1" ]; then
    cp -arfLv /lib/ld-linux-aarch64.so.1 "$CURDIR/package/lib/ld.so"
else
    echo "package.sh: unknown architecture" >&2; exit 1
fi

# Bundle the binary's transitive shared deps (libstdc++, libgomp, and the apt
# grpc++/protobuf/absl stack) by walking ldd — robust to whichever of those are
# linked shared vs static. The loader line (no "=>") is skipped; ld.so above
# already covers it.
ldd "$CURDIR/grpc-server" | awk '$2 == "=>" && $3 ~ /^\// { print $3 }' | sort -u | \
while read -r so; do
    [ -f "$so" ] && cp -arfLv "$so" "$CURDIR/package/lib/"
done

# Vulkan loader / GPU libs when building the GPU variant.
GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "privacy-filter package contents:"
ls -lah "$CURDIR/package/" "$CURDIR/package/lib/"
