# Built as a subdirectory of gufo's own tree (patch 0003 adds the hook), so it
# reuses gufo's toolchain flags, HIP setup and its "src/..." include root.
cmake_minimum_required(VERSION 3.21)

add_library(gufo_engine STATIC gufo_engine.cpp)
target_include_directories(gufo_engine PUBLIC ${CMAKE_CURRENT_SOURCE_DIR})
target_link_libraries(gufo_engine PUBLIC gufo_runtime gufo_diagnostics)

option(GUFO_BACKEND_BUILD_GRPC "Build the gRPC server (needs gRPC and protobuf)" ON)
if(GUFO_BACKEND_BUILD_GRPC)
  find_package(Threads REQUIRED)
  find_package(Protobuf CONFIG QUIET)
  if(NOT Protobuf_FOUND)
    find_package(Protobuf REQUIRED)
  endif()
  find_package(gRPC CONFIG QUIET)
  if(NOT gRPC_FOUND)
    # Ubuntu's apt grpc++ ships no CMake config.
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
  set(HW_GRPC_SRCS "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.cc")
  set(HW_GRPC_HDRS "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.h")
  add_custom_command(
    OUTPUT ${HW_PROTO_SRCS} ${HW_PROTO_HDRS} ${HW_GRPC_SRCS} ${HW_GRPC_HDRS}
    COMMAND ${_PROTOC}
    ARGS --grpc_out "${CMAKE_CURRENT_BINARY_DIR}" --cpp_out "${CMAKE_CURRENT_BINARY_DIR}"
         -I "${HW_PROTO_PATH}" --plugin=protoc-gen-grpc="${_GRPC_CPP_PLUGIN}" "${HW_PROTO}"
    DEPENDS "${HW_PROTO}")
  add_library(hw_grpc_proto STATIC ${HW_GRPC_SRCS} ${HW_GRPC_HDRS} ${HW_PROTO_SRCS} ${HW_PROTO_HDRS})
  target_include_directories(hw_grpc_proto PUBLIC ${CMAKE_CURRENT_BINARY_DIR})
  target_link_libraries(hw_grpc_proto PUBLIC protobuf::libprotobuf gRPC::grpc++)

  add_executable(grpc-server grpc-server.cpp)
  target_link_libraries(grpc-server PRIVATE gufo_engine hw_grpc_proto gRPC::grpc++ gRPC::grpc++_reflection
                                            protobuf::libprotobuf Threads::Threads)
endif()
