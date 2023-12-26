
set(CMAKE_CXX_STANDARD 17)
cmake_minimum_required(VERSION 3.15)

include(CMakePrintHelpers)

## XXX: In some versions of CMake clip wasn't being built before llama.
## This is an hack for now, but it should be fixed in the future.
set(TARGET myclip)
add_library(${TARGET} clip.cpp clip.h)
install(TARGETS ${TARGET} LIBRARY)
target_link_libraries(${TARGET} PRIVATE common ggml ${CMAKE_THREAD_LIBS_INIT})
target_compile_features(${TARGET} PRIVATE cxx_std_11)
if (NOT MSVC)
    target_compile_options(${TARGET} PRIVATE -Wno-cast-qual) # stb_image.h
endif()
# END CLIP hack

set(TARGET grpc-server)
set(_PROTOBUF_LIBPROTOBUF libprotobuf)
set(_REFLECTION grpc++_reflection)
set(_GRPC_GRPCPP grpc++)

if (${CMAKE_SYSTEM_NAME} MATCHES "Darwin")
    link_directories("/opt/homebrew/lib")
    include_directories("/opt/homebrew/include")
endif()

# find_package(absl CONFIG REQUIRED)
find_package(Protobuf CONFIG REQUIRED)
find_package(gRPC CONFIG REQUIRED)

if(NOT DEFINED _PROTOBUF_PROTOC)
  message(STATUS "_PROTOBUF_PROTOC not set, defaulting via find_program")
  find_program(_PROTOBUF_PROTOC protoc)
  cmake_print_variables(_PROTOBUF_PROTOC)
endif()
if(NOT DEFINED _GRPC_CPP_PLUGIN_EXECUTABLE)
  message(STATUS "_GRPC_CPP_PLUGIN_EXECUTABLE not set, defaulting via find_program")
  find_program(_GRPC_CPP_PLUGIN_EXECUTABLE grpc_cpp_plugin)
  cmake_print_variables(_GRPC_CPP_PLUGIN_EXECUTABLE)
endif()

include_directories(${Protobuf_INCLUDE_DIRS})
include_directories(${CMAKE_CURRENT_BINARY_DIR})

message(STATUS "=== backend/cpp/llama/CMakeLists.txt main variable dump ===")
cmake_print_variables(Protobuf_VERSION Protobuf_INCLUDE_DIRS CMAKE_CURRENT_BINARY_DIR C_INCLUDE_PATH Protobuf_INCLUDE_DIR _PROTOBUF_PROTOC _GRPC_CPP_PLUGIN_EXECUTABLE)


# Proto file
get_filename_component(hw_proto "../../../../../../backend/backend.proto" ABSOLUTE)
get_filename_component(hw_proto_path "${hw_proto}" PATH)

# Generated sources
set(hw_proto_srcs "${CMAKE_CURRENT_BINARY_DIR}/backend.pb.cc")
set(hw_proto_hdrs "${CMAKE_CURRENT_BINARY_DIR}/backend.pb.h")
set(hw_grpc_srcs "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.cc")
set(hw_grpc_hdrs "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.h")

add_custom_command(
      OUTPUT "${hw_proto_srcs}" "${hw_proto_hdrs}" "${hw_grpc_srcs}" "${hw_grpc_hdrs}"
      COMMAND ${_PROTOBUF_PROTOC} --grpc_out "${CMAKE_CURRENT_BINARY_DIR}" --cpp_out "${CMAKE_CURRENT_BINARY_DIR}" -I "${hw_proto_path}" --plugin=protoc-gen-grpc="${_GRPC_CPP_PLUGIN_EXECUTABLE}" "${hw_proto}"
      DEPENDS "${hw_proto}")

# hw_grpc_proto
add_library(hw_grpc_proto
  ${hw_grpc_srcs}
  ${hw_grpc_hdrs}
  ${hw_proto_srcs}
  ${hw_proto_hdrs} )

add_executable(${TARGET} grpc-server.cpp json.hpp )
target_link_libraries(${TARGET} PRIVATE common llama myclip ${CMAKE_THREAD_LIBS_INIT} absl::flags hw_grpc_proto
  absl::flags_parse
  gRPC::${_REFLECTION}
  gRPC::${_GRPC_GRPCPP}
  protobuf::${_PROTOBUF_LIBPROTOBUF})
target_compile_features(${TARGET} PRIVATE cxx_std_11)
if(TARGET BUILD_INFO)
  add_dependencies(${TARGET} BUILD_INFO)
endif()