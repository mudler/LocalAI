# SPDX-License-Identifier: MIT
cmake_minimum_required(VERSION 3.14)
project(localai_native_strict C CXX)
if(NOT IS_DIRECTORY "${LLAMA_SOURCE_DIR}/common")
    message(FATAL_ERROR "LLAMA_SOURCE_DIR must name the pinned llama.cpp source")
endif()
add_subdirectory("${LLAMA_SOURCE_DIR}" pinned)
add_executable(native-strict ../native-strict.cpp)
target_link_libraries(native-strict PRIVATE llama-common llama)
target_compile_features(native-strict PRIVATE cxx_std_17)
enable_testing()
add_test(NAME native-strict COMMAND native-strict)

add_executable(native-schema ../native-schema.cpp)
target_include_directories(native-schema PRIVATE "${LLAMA_SOURCE_DIR}/src")
target_link_libraries(native-schema PRIVATE llama-common llama)
target_compile_features(native-schema PRIVATE cxx_std_17)

add_test(NAME native-schema COMMAND native-schema
    "${CMAKE_CURRENT_SOURCE_DIR}/../fixtures/sound-schema.json"
    "${CMAKE_CURRENT_BINARY_DIR}/native-schema-output")

add_executable(native-edge ../native-edge.cpp)
target_include_directories(native-edge PRIVATE "${LLAMA_SOURCE_DIR}/src")
target_link_libraries(native-edge PRIVATE llama-common llama)
target_compile_features(native-edge PRIVATE cxx_std_17)
add_test(NAME native-edge COMMAND native-edge)
