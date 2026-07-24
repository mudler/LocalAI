# SPDX-License-Identifier: MIT

cmake_minimum_required(VERSION 3.20)
project(audio-cpp-grpc-server LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

set(AUDIO_CPP_DIR "${CMAKE_CURRENT_SOURCE_DIR}/audio.cpp"
    CACHE PATH "Path to the audio.cpp source tree")
set(LOCALAI_BACKEND_PROTO "${CMAKE_CURRENT_SOURCE_DIR}/../../backend.proto"
    CACHE FILEPATH "Path to the LocalAI backend protocol")

option(ENGINE_ENABLE_CUDA "Build audio.cpp with CUDA support" OFF)
option(ENGINE_ENABLE_VULKAN "Build audio.cpp with Vulkan support" OFF)
option(ENGINE_ENABLE_METAL "Build audio.cpp with Metal support" OFF)

find_package(Protobuf CONFIG REQUIRED)
find_package(gRPC CONFIG REQUIRED)
find_program(PROTOC_EXECUTABLE NAMES protoc REQUIRED)
find_program(GRPC_CPP_PLUGIN_EXECUTABLE NAMES grpc_cpp_plugin REQUIRED)

get_filename_component(LOCALAI_BACKEND_PROTO_DIR
    "${LOCALAI_BACKEND_PROTO}" DIRECTORY)
set(LOCALAI_PROTO_SOURCES
    "${CMAKE_CURRENT_BINARY_DIR}/backend.pb.cc"
    "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.cc")
set(LOCALAI_PROTO_HEADERS
    "${CMAKE_CURRENT_BINARY_DIR}/backend.pb.h"
    "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.h")

add_custom_command(
    OUTPUT ${LOCALAI_PROTO_SOURCES} ${LOCALAI_PROTO_HEADERS}
    COMMAND "${PROTOC_EXECUTABLE}"
    ARGS
      --cpp_out "${CMAKE_CURRENT_BINARY_DIR}"
      --grpc_out "${CMAKE_CURRENT_BINARY_DIR}"
      -I "${LOCALAI_BACKEND_PROTO_DIR}"
      --plugin=protoc-gen-grpc="${GRPC_CPP_PLUGIN_EXECUTABLE}"
      "${LOCALAI_BACKEND_PROTO}"
    DEPENDS "${LOCALAI_BACKEND_PROTO}"
    VERBATIM)

add_library(localai_backend_proto STATIC
    ${LOCALAI_PROTO_SOURCES}
    ${LOCALAI_PROTO_HEADERS})
target_include_directories(localai_backend_proto
    PUBLIC "${CMAKE_CURRENT_BINARY_DIR}")
target_link_libraries(localai_backend_proto
    PUBLIC protobuf::libprotobuf gRPC::grpc++)

if(NOT EXISTS "${AUDIO_CPP_DIR}/CMakeLists.txt")
  message(FATAL_ERROR
      "AUDIO_CPP_DIR does not point to an audio.cpp source tree: ${AUDIO_CPP_DIR}")
endif()
add_subdirectory("${AUDIO_CPP_DIR}" "${CMAKE_CURRENT_BINARY_DIR}/audio.cpp")

# Task 2 replaces this generated entry point with the LocalAI service.
set(AUDIO_CPP_SERVER_PLACEHOLDER
    "${CMAKE_CURRENT_BINARY_DIR}/audio-cpp-grpc-server-placeholder.cpp")
file(GENERATE OUTPUT "${AUDIO_CPP_SERVER_PLACEHOLDER}"
    CONTENT "int main() { return 0; }\n")

add_executable(audio-cpp-grpc-server "${AUDIO_CPP_SERVER_PLACEHOLDER}")
target_link_libraries(audio-cpp-grpc-server PRIVATE
    engine_runtime
    localai_backend_proto
    gRPC::grpc++
    gRPC::grpc++_reflection)
