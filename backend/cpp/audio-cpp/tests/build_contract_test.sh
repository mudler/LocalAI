#!/usr/bin/env bash
# SPDX-License-Identifier: MIT

set -euo pipefail

backend_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

fixture_dir="${tmp_dir}/audio.cpp"
prefix_dir="${tmp_dir}/prefix"
tools_dir="${tmp_dir}/tools"
mkdir -p "${fixture_dir}" "${prefix_dir}/lib/cmake/Protobuf" \
  "${prefix_dir}/lib/cmake/gRPC" "${tools_dir}"

cat >"${fixture_dir}/engine_runtime.cpp" <<'EOF'
void audio_cpp_build_contract_fixture() {}
EOF

cat >"${fixture_dir}/CMakeLists.txt" <<'EOF'
cmake_minimum_required(VERSION 3.20)
project(AudioCppBuildContractFixture LANGUAGES CXX)

option(ENGINE_ENABLE_CUDA "Build with CUDA" OFF)
option(ENGINE_ENABLE_VULKAN "Build with Vulkan" OFF)
option(ENGINE_ENABLE_METAL "Build with Metal" OFF)

foreach(wrong_option IN ITEMS
    AUDIO_CPP_ENABLE_CUDA
    AUDIO_CPP_ENABLE_VULKAN
    AUDIO_CPP_ENABLE_METAL
    AUDIOCPP_ENABLE_CUDA
    AUDIOCPP_ENABLE_VULKAN
    AUDIOCPP_ENABLE_METAL
    ENGINE_CUDA
    ENGINE_VULKAN
    ENGINE_METAL
    GGML_CUDA
    GGML_VULKAN
    GGML_METAL)
  if(DEFINED ${wrong_option})
    message(FATAL_ERROR "legacy or unsupported audio.cpp option: ${wrong_option}")
  endif()
endforeach()

add_library(engine_runtime STATIC engine_runtime.cpp)
EOF

cat >"${prefix_dir}/lib/cmake/Protobuf/ProtobufConfig.cmake" <<'EOF'
set(Protobuf_FOUND TRUE)
set(Protobuf_VERSION 0.0.0)
if(NOT TARGET protobuf::libprotobuf)
  add_library(protobuf::libprotobuf INTERFACE IMPORTED)
endif()
EOF

cat >"${prefix_dir}/lib/cmake/gRPC/gRPCConfig.cmake" <<'EOF'
set(gRPC_FOUND TRUE)
if(NOT TARGET gRPC::grpc++)
  add_library(gRPC::grpc++ INTERFACE IMPORTED)
endif()
if(NOT TARGET gRPC::grpc++_reflection)
  add_library(gRPC::grpc++_reflection INTERFACE IMPORTED)
endif()
EOF

cat >"${tools_dir}/protoc" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
cat >"${tools_dir}/grpc_cpp_plugin" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
chmod +x "${tools_dir}/protoc" "${tools_dir}/grpc_cpp_plugin"

assert_cache_bool() {
  local cache_file="$1"
  local name="$2"
  local expected="$3"
  grep -q "^${name}:BOOL=${expected}$" "${cache_file}" || {
    echo "expected ${name}:BOOL=${expected} in ${cache_file}" >&2
    return 1
  }
}

configure_case() {
  local name="$1"
  local cuda="$2"
  local vulkan="$3"
  local metal="$4"
  local build_dir="${tmp_dir}/build-${name}"

  PATH="${tools_dir}:${PATH}" cmake \
    -S "${backend_dir}" \
    -B "${build_dir}" \
    -DCMAKE_PREFIX_PATH="${prefix_dir}" \
    -DAUDIO_CPP_DIR="${fixture_dir}" \
    -DENGINE_ENABLE_CUDA="${cuda}" \
    -DENGINE_ENABLE_VULKAN="${vulkan}" \
    -DENGINE_ENABLE_METAL="${metal}" \
    >/dev/null

  assert_cache_bool "${build_dir}/CMakeCache.txt" ENGINE_ENABLE_CUDA "${cuda}"
  assert_cache_bool "${build_dir}/CMakeCache.txt" ENGINE_ENABLE_VULKAN "${vulkan}"
  assert_cache_bool "${build_dir}/CMakeCache.txt" ENGINE_ENABLE_METAL "${metal}"

  grep -q 'engine_runtime' \
    "${build_dir}/CMakeFiles/audio-cpp-grpc-server.dir/link.txt" || {
    echo "audio-cpp-grpc-server does not link engine_runtime" >&2
    return 1
  }
}

configure_case cpu OFF OFF OFF
configure_case cuda ON OFF OFF
configure_case vulkan OFF ON OFF
configure_case metal OFF OFF ON

if PATH="${tools_dir}:${PATH}" cmake \
  -S "${backend_dir}" \
  -B "${tmp_dir}/build-wrong-option" \
  -DCMAKE_PREFIX_PATH="${prefix_dir}" \
  -DAUDIO_CPP_DIR="${fixture_dir}" \
  -DGGML_CUDA=ON \
  >/dev/null 2>&1; then
  echo "strict audio.cpp fixture accepted legacy GGML_CUDA option" >&2
  exit 1
fi

make_database="${tmp_dir}/make-database"
make -C "${backend_dir}" -pn >"${make_database}"
audio_cpp_version="$(
  sed -n 's/^AUDIO_CPP_VERSION = //p' "${make_database}" | head -n 1
)"
[[ "${audio_cpp_version}" =~ ^[0-9a-f]{40}$ ]] || {
  echo "AUDIO_CPP_VERSION must be a pinned 40-character commit" >&2
  exit 1
}

fetch_plan="${tmp_dir}/fetch-plan"
make -C "${backend_dir}" -Bn audio.cpp >"${fetch_plan}"
grep -q 'github.com/0xShug0/audio.cpp' "${fetch_plan}"
grep -q "${audio_cpp_version}" "${fetch_plan}"

cat >"${tools_dir}/uname" <<'EOF'
#!/usr/bin/env sh
if [ "$#" -eq 1 ] && [ "$1" = "-s" ]; then
  echo Darwin
  exit 0
fi
echo "build contract requires uname -s" >&2
exit 64
EOF
chmod +x "${tools_dir}/uname"

darwin_plan="${tmp_dir}/darwin-plan"
PATH="${tools_dir}:${PATH}" make -C "${backend_dir}" -n \
  AUDIO_CPP_SRC="${fixture_dir}" grpc-server >"${darwin_plan}"
grep -q -- '-DENGINE_ENABLE_CUDA=OFF' "${darwin_plan}"
grep -q -- '-DENGINE_ENABLE_VULKAN=OFF' "${darwin_plan}"
grep -q -- '-DENGINE_ENABLE_METAL=ON' "${darwin_plan}"

echo "audio.cpp build contract: PASS"
