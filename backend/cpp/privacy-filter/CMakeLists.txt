cmake_minimum_required(VERSION 3.21)
project(privacy-filter-grpc-server LANGUAGES CXX C)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(TARGET grpc-server)

# Path to the privacy-filter.cpp engine sources. The Makefile arranges for this
# to exist (clone of a pinned commit, or a symlink to PRIVACY_FILTER_SRC).
set(PRIVACY_FILTER_DIR "${CMAKE_CURRENT_SOURCE_DIR}/privacy-filter.cpp"
    CACHE PATH "Path to the privacy-filter.cpp engine source tree")

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

# Build only the pf static lib (+ ggml) from the engine tree — no CLI/bench/tests.
# PF_VULKAN is honored when passed on the cmake command line (it lands in the
# shared cache the engine reads).
set(PF_BUILD_TOOLS OFF CACHE BOOL "" FORCE)
set(PF_BUILD_TESTS OFF CACHE BOOL "" FORCE)
add_subdirectory(${PRIVACY_FILTER_DIR} ${CMAKE_CURRENT_BINARY_DIR}/privacy-filter.cpp)

add_executable(${TARGET} grpc-server.cpp)
target_link_libraries(${TARGET} PRIVATE
    pf
    hw_grpc_proto
    gRPC::grpc++
    gRPC::grpc++_reflection
    protobuf::libprotobuf
    Threads::Threads)
