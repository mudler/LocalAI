set(CMAKE_CXX_STANDARD 17)
cmake_minimum_required(VERSION 3.15)
set(TARGET grpc-server)
set(_PROTOBUF_LIBPROTOBUF libprotobuf)
set(_REFLECTION grpc++_reflection)

option(USE_SYSTEM_GRPC "Use system installed gRPC" OFF)

if(USE_SYSTEM_GRPC)
  # Find system-installed gRPC
  find_package(gRPC CONFIG REQUIRED)
else()
  include(FetchContent)
  FetchContent_Declare(
    gRPC
    GIT_REPOSITORY https://github.com/grpc/grpc
    GIT_TAG        v1.58.0
  )
  set(FETCHCONTENT_QUIET OFF)
  FetchContent_MakeAvailable(gRPC)
endif()

find_package(Protobuf CONFIG REQUIRED)
find_package(absl CONFIG REQUIRED)

find_program(_PROTOBUF_PROTOC protoc)
set(_GRPC_GRPCPP grpc++)
find_program(_GRPC_CPP_PLUGIN_EXECUTABLE grpc_cpp_plugin)

include_directories(${CMAKE_CURRENT_BINARY_DIR})
include_directories(${Protobuf_INCLUDE_DIRS})

message(STATUS "Using protobuf ${Protobuf_VERSION} ${Protobuf_INCLUDE_DIRS} ${CMAKE_CURRENT_BINARY_DIR}")


# Proto file
get_filename_component(hw_proto "../../../../../../pkg/grpc/proto/backend.proto" ABSOLUTE)
get_filename_component(hw_proto_path "${hw_proto}" PATH)

# Generated sources
set(hw_proto_srcs "${CMAKE_CURRENT_BINARY_DIR}/backend.pb.cc")
set(hw_proto_hdrs "${CMAKE_CURRENT_BINARY_DIR}/backend.pb.h")
set(hw_grpc_srcs "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.cc")
set(hw_grpc_hdrs "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.h")

add_custom_command(
      OUTPUT "${hw_proto_srcs}" "${hw_proto_hdrs}" "${hw_grpc_srcs}" "${hw_grpc_hdrs}"
      COMMAND ${_PROTOBUF_PROTOC}
      ARGS --grpc_out "${CMAKE_CURRENT_BINARY_DIR}"
        --cpp_out "${CMAKE_CURRENT_BINARY_DIR}"
        -I "${hw_proto_path}"
        --plugin=protoc-gen-grpc="${_GRPC_CPP_PLUGIN_EXECUTABLE}"
        "${hw_proto}"
      DEPENDS "${hw_proto}")

# hw_grpc_proto
add_library(hw_grpc_proto
  ${hw_grpc_srcs}
  ${hw_grpc_hdrs}
  ${hw_proto_srcs}
  ${hw_proto_hdrs})

add_executable(${TARGET} grpc-server.cpp)
target_link_libraries(${TARGET} PRIVATE common llama ${CMAKE_THREAD_LIBS_INIT} absl::flags hw_grpc_proto
  absl::flags_parse
  gRPC::${_REFLECTION}
  gRPC::${_GRPC_GRPCPP}
  protobuf::${_PROTOBUF_LIBPROTOBUF})
target_compile_features(${TARGET} PRIVATE cxx_std_11)
if(TARGET BUILD_INFO)
  add_dependencies(${TARGET} BUILD_INFO)
endif()