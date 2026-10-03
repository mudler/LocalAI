#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# CPU validation against an already prepared stock checkout. No downloads.
# Run prepare.sh on a clean checkout first. DEPS_ROOT is an optional extracted
# distro /usr tree parent, not a system installation. Output remains local.
set -euo pipefail
root=$(git rev-parse --show-toplevel)
backend="$root/backend/cpp/llama-cpp"
source="$backend/llama.cpp"
out=${OUT_DIR:-"$source/build-decision-validation"}
mkdir -p "$out"
deps=${DEPS_ROOT:-/}
export LD_LIBRARY_PATH="$deps/usr/lib/x86_64-linux-gnu${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export PKG_CONFIG_SYSROOT_DIR="$deps"
export PKG_CONFIG_PATH="$deps/usr/lib/x86_64-linux-gnu/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
# Build upstream libraries without the optional grpc CMake subdirectory: distro
# protobuf packages may expose FindProtobuf rather than ProtobufConfig.cmake.
# An existing CPU build can be supplied to avoid rebuilding them.
build=${CPU_BUILD:?set CPU_BUILD to the patched upstream CPU build directory}
protoc -I "$root/backend" --cpp_out="$out" --grpc_out="$out" \
  --plugin=protoc-gen-grpc="$deps/usr/bin/grpc_cpp_plugin" "$root/backend/backend.proto"
protoc -I "$root/backend" --python_out="$out" --grpc_out="$out" \
  --plugin=protoc-gen-grpc="$deps/usr/bin/grpc_python_plugin" "$root/backend/backend.proto"
includes=(-I"$out" -I"$deps/usr/include")
for dir in '' include common vendor ggml/include tools/mtmd tools/server; do
  includes+=(-I"$source/$dir")
done
cxx=${CXX:-g++}
"$cxx" -O0 -std=c++17 -pthread "${includes[@]}" -c "$source/tools/grpc-server/grpc-server.cpp" -o "$out/grpc-server.o"
for file in backend.pb backend.grpc.pb; do
  "$cxx" -O0 -std=c++17 -pthread "${includes[@]}" -c "$out/$file.cc" -o "$out/$file.o"
done
image_libs=()
if [[ -f "$source/tools/server/server-decision.cpp" ]]; then
  image_libs=(-lz -ljpeg)
fi
libs=()
for lib in common/llama-common common/llama-common-base tools/mtmd/mtmd src/llama ggml/src/ggml ggml/src/ggml-cpu ggml/src/ggml-base vendor/hash/vendor-hash vendor/cpp-httplib/cpp-httplib; do
  libs+=("$build/${lib%/*}/lib${lib##*/}.a")
done
# pkg-config emits a linker flag list, so intentional word splitting here.
# shellcheck disable=SC2046
"$cxx" -pthread "$out/grpc-server.o" "$out/backend.pb.o" "$out/backend.grpc.pb.o" "${libs[@]}" \
  -L"$deps/usr/lib/x86_64-linux-gnu" -Wl,-rpath-link,"$deps/usr/lib/x86_64-linux-gnu" \
  -lgrpc++_reflection $(pkg-config --libs grpc++) \
  -labsl_flags_parse -labsl_flags_usage -labsl_flags_usage_internal \
  -labsl_flags_commandlineflag -labsl_flags_commandlineflag_internal \
  -labsl_flags_config -labsl_flags_internal -labsl_flags_reflection \
  -labsl_flags_marshalling -lprotobuf "${image_libs[@]}" -ldl -lm -lgomp -o "$out/grpc-server"
printf 'Built %s\n' "$out/grpc-server"
