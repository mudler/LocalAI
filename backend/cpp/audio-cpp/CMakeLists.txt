cmake_minimum_required(VERSION 3.20)
project(audio-cpp-grpc-server LANGUAGES C CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(TARGET grpc-server)

set(AUDIO_CPP_DIR "${CMAKE_CURRENT_SOURCE_DIR}/audio.cpp"
    CACHE PATH "Path to the pinned audio.cpp checkout")
option(AUDIO_CPP_GRPC_BUILD_TESTS "Build engine-linked ctest binaries" OFF)

if(NOT EXISTS "${AUDIO_CPP_DIR}/CMakeLists.txt")
    message(FATAL_ERROR
        "AUDIO_CPP_DIR does not contain an audio.cpp checkout: ${AUDIO_CPP_DIR}. "
        "Run 'make audio.cpp' first.")
endif()

if(APPLE)
    # Homebrew installs protobuf/grpc under a non-default prefix.
    if(CMAKE_HOST_SYSTEM_PROCESSOR MATCHES "arm64")
        set(HOMEBREW_DEFAULT_PREFIX "/opt/homebrew")
    else()
        set(HOMEBREW_DEFAULT_PREFIX "/usr/local")
    endif()
    link_directories("${HOMEBREW_DEFAULT_PREFIX}/lib")
    include_directories("${HOMEBREW_DEFAULT_PREFIX}/include")
endif()

find_package(Threads REQUIRED)
find_package(Protobuf CONFIG QUIET)
if(NOT Protobuf_FOUND)
    find_package(Protobuf REQUIRED)
endif()
find_package(gRPC CONFIG QUIET)
if(NOT gRPC_FOUND)
    # Ubuntu's apt-installed grpc++ ships no CMake config. Fall back.
    find_library(GRPCPP_LIB grpc++ REQUIRED)
    find_library(GRPCPP_REFLECTION_LIB grpc++_reflection REQUIRED)
    add_library(gRPC::grpc++ INTERFACE IMPORTED)
    set_target_properties(gRPC::grpc++ PROPERTIES
        INTERFACE_LINK_LIBRARIES "${GRPCPP_LIB}")
    add_library(gRPC::grpc++_reflection INTERFACE IMPORTED)
    set_target_properties(gRPC::grpc++_reflection PROPERTIES
        INTERFACE_LINK_LIBRARIES "${GRPCPP_REFLECTION_LIB}")
endif()

find_program(_PROTOC NAMES protoc REQUIRED)
find_program(_GRPC_CPP_PLUGIN NAMES grpc_cpp_plugin REQUIRED)

get_filename_component(HW_PROTO "${CMAKE_CURRENT_SOURCE_DIR}/../../backend.proto" ABSOLUTE)
get_filename_component(HW_PROTO_PATH "${HW_PROTO}" PATH)

set(HW_PROTO_SRCS "${CMAKE_CURRENT_BINARY_DIR}/backend.pb.cc")
set(HW_PROTO_HDRS "${CMAKE_CURRENT_BINARY_DIR}/backend.pb.h")
set(HW_GRPC_SRCS  "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.cc")
set(HW_GRPC_HDRS  "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.h")

add_custom_command(
    OUTPUT "${HW_PROTO_SRCS}" "${HW_PROTO_HDRS}" "${HW_GRPC_SRCS}" "${HW_GRPC_HDRS}"
    COMMAND ${_PROTOC}
    ARGS --grpc_out "${CMAKE_CURRENT_BINARY_DIR}"
         --cpp_out  "${CMAKE_CURRENT_BINARY_DIR}"
         -I "${HW_PROTO_PATH}"
         --plugin=protoc-gen-grpc="${_GRPC_CPP_PLUGIN}"
         "${HW_PROTO}"
    DEPENDS "${HW_PROTO}")

add_library(hw_grpc_proto STATIC
    ${HW_GRPC_SRCS} ${HW_GRPC_HDRS}
    ${HW_PROTO_SRCS} ${HW_PROTO_HDRS})
target_include_directories(hw_grpc_proto PUBLIC ${CMAKE_CURRENT_BINARY_DIR})
# Required on macOS: without these the Homebrew protobuf/grpc include dirs never
# reach this target and google/protobuf/runtime_version.h is not found.
target_link_libraries(hw_grpc_proto PUBLIC protobuf::libprotobuf gRPC::grpc++)

# Upstream's global add_compile_options(-Wall -Wextra -Wpedantic -pedantic-errors)
# is a directory property of the subdirectory and does not reach our targets.
add_subdirectory("${AUDIO_CPP_DIR}" "${CMAKE_CURRENT_BINARY_DIR}/audio-cpp" EXCLUDE_FROM_ALL)

add_executable(${TARGET}
    grpc-server.cpp
)

target_include_directories(${TARGET} PRIVATE
    "${AUDIO_CPP_DIR}/include"
    "${CMAKE_CURRENT_SOURCE_DIR}")

target_link_libraries(${TARGET} PRIVATE
    hw_grpc_proto
    engine_runtime
    ggml
    gRPC::grpc++
    gRPC::grpc++_reflection
    protobuf::libprotobuf
    Threads::Threads)

# ENGINE_ENABLE_CPU_ALL_VARIANTS builds ggml backends as shared objects that sit
# next to the binary in the package, so the binary must search its own directory.
set_target_properties(${TARGET} PROPERTIES
    BUILD_RPATH "$ORIGIN"
    INSTALL_RPATH "$ORIGIN")

if(AUDIO_CPP_GRPC_BUILD_TESTS)
    enable_testing()
endif()
