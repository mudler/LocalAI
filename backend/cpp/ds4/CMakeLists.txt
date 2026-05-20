cmake_minimum_required(VERSION 3.15)
project(ds4-grpc-server LANGUAGES CXX C)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(TARGET grpc-server)

option(DS4_NATIVE "Compile with -march=native / -mcpu=native" ON)
set(DS4_GPU "cpu" CACHE STRING "GPU backend: cpu, cuda, or metal")
set(DS4_DIR "${CMAKE_CURRENT_SOURCE_DIR}/ds4" CACHE PATH "Path to cloned ds4 source")

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
