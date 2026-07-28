cmake_minimum_required(VERSION 3.15)
project(ds4-grpc-server LANGUAGES CXX C)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(TARGET grpc-server)

option(DS4_NATIVE "Compile with -march=native / -mcpu=native" ON)
set(DS4_GPU "cpu" CACHE STRING "GPU backend: cpu, cuda, or metal")
set(DS4_DIR "${CMAKE_CURRENT_SOURCE_DIR}/ds4" CACHE PATH "Path to cloned ds4 source")

if(${CMAKE_SYSTEM_NAME} MATCHES "Darwin")
    # Homebrew installs protobuf/grpc under a non-default prefix. The generated
    # backend.pb.cc / backend.grpc.pb.cc pull in google/protobuf and grpcpp
    # headers, but the hw_grpc_proto library links neither target, so on macOS
    # the headers (e.g. google/protobuf/runtime_version.h) are never on the
    # compiler's include path. Add the Homebrew prefix globally, matching the
    # llama-cpp backend which builds on Darwin CI.
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
    # Ubuntu's apt-installed grpc++ does not ship a CMake config - fall back.
    find_library(GRPCPP_LIB grpc++ REQUIRED)
    find_library(GRPCPP_REFLECTION_LIB grpc++_reflection REQUIRED)
    add_library(gRPC::grpc++ INTERFACE IMPORTED)
    set_target_properties(gRPC::grpc++ PROPERTIES INTERFACE_LINK_LIBRARIES "${GRPCPP_LIB}")
    add_library(gRPC::grpc++_reflection INTERFACE IMPORTED)
    set_target_properties(gRPC::grpc++_reflection PROPERTIES INTERFACE_LINK_LIBRARIES "${GRPCPP_REFLECTION_LIB}")
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

set(DS4_OBJS "${DS4_DIR}/ds4.o")
if(DS4_GPU STREQUAL "cuda")
    list(APPEND DS4_OBJS "${DS4_DIR}/ds4_cuda.o")
elseif(DS4_GPU STREQUAL "metal")
    list(APPEND DS4_OBJS "${DS4_DIR}/ds4_metal.o")
elseif(DS4_GPU STREQUAL "cpu")
    set(DS4_OBJS "${DS4_DIR}/ds4_cpu.o")
endif()

# Upstream splits distributed inference, tensor-parallel transport, the SSD
# expert cache, and layer placement into GPU-agnostic translation units. Link
# them regardless of DS4_GPU.
list(APPEND DS4_OBJS "${DS4_DIR}/ds4_distributed.o")
list(APPEND DS4_OBJS "${DS4_DIR}/ds4_tp.o")
list(APPEND DS4_OBJS "${DS4_DIR}/ds4_ssd.o")
list(APPEND DS4_OBJS "${DS4_DIR}/ds4_layer_pack.o")

add_executable(${TARGET}
    grpc-server.cpp
    dsml_parser.cpp
    dsml_renderer.cpp
    kv_cache.cpp)

target_include_directories(${TARGET} PRIVATE ${DS4_DIR})

foreach(obj ${DS4_OBJS})
    target_sources(${TARGET} PRIVATE ${obj})
    set_source_files_properties(${obj} PROPERTIES EXTERNAL_OBJECT TRUE GENERATED TRUE)
endforeach()

target_link_libraries(${TARGET} PRIVATE
    hw_grpc_proto
    gRPC::grpc++
    gRPC::grpc++_reflection
    protobuf::libprotobuf
    Threads::Threads
    m)

if(DS4_GPU STREQUAL "cuda")
    find_package(CUDAToolkit REQUIRED)
    target_link_libraries(${TARGET} PRIVATE CUDA::cudart CUDA::cublas)
elseif(DS4_GPU STREQUAL "metal")
    find_library(FOUNDATION_LIB Foundation REQUIRED)
    find_library(METAL_LIB Metal REQUIRED)
    target_link_libraries(${TARGET} PRIVATE ${FOUNDATION_LIB} ${METAL_LIB})
elseif(DS4_GPU STREQUAL "cpu")
    target_compile_definitions(${TARGET} PRIVATE DS4_NO_GPU)
endif()

if(DS4_NATIVE)
    if(APPLE)
        target_compile_options(${TARGET} PRIVATE -mcpu=native)
    else()
        target_compile_options(${TARGET} PRIVATE -march=native)
    endif()
endif()

# ds4-worker: standalone distributed worker. Links the same ds4 engine objects
# (including ds4_distributed.o) but has NO gRPC/protobuf dependency - it speaks
# ds4's own TCP transport via ds4_dist_run(). Buildable wherever the engine
# objects build, even on hosts without protobuf/grpc dev headers.
add_executable(ds4-worker worker_main.c)
target_include_directories(ds4-worker PRIVATE ${DS4_DIR})
foreach(obj ${DS4_OBJS})
    target_sources(ds4-worker PRIVATE ${obj})
    set_source_files_properties(${obj} PROPERTIES EXTERNAL_OBJECT TRUE GENERATED TRUE)
endforeach()
# worker_main.c is C, but the engine objects built by nvcc (ds4_cuda.o) and the
# Metal path (ds4_metal.o, Obj-C++) reference the C++ runtime (libstdc++). Force
# the C++ linker driver so those symbols resolve; the C driver would not link
# libstdc++ and the CUDA/Metal builds fail with undefined std:: references.
set_target_properties(ds4-worker PROPERTIES LINKER_LANGUAGE CXX)
target_link_libraries(ds4-worker PRIVATE Threads::Threads m)

if(DS4_GPU STREQUAL "cuda")
    target_link_libraries(ds4-worker PRIVATE CUDA::cudart CUDA::cublas)
elseif(DS4_GPU STREQUAL "metal")
    target_link_libraries(ds4-worker PRIVATE ${FOUNDATION_LIB} ${METAL_LIB})
elseif(DS4_GPU STREQUAL "cpu")
    target_compile_definitions(ds4-worker PRIVATE DS4_NO_GPU)
endif()

if(DS4_NATIVE)
    if(APPLE)
        target_compile_options(ds4-worker PRIVATE -mcpu=native)
    else()
        target_compile_options(ds4-worker PRIVATE -march=native)
    endif()
endif()
