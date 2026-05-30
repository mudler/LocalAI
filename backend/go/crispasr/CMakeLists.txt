cmake_minimum_required(VERSION 3.12)
project(gocrispasr LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

add_subdirectory(./sources/CrispASR)

add_library(gocrispasr MODULE cpp/crispasr_shim.cpp)
target_include_directories(gocrispasr PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/sources/CrispASR/include
    ${CMAKE_CURRENT_SOURCE_DIR}/sources/CrispASR/ggml/include)
target_link_libraries(gocrispasr PRIVATE crispasr ggml)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(gocrispasr PRIVATE stdc++fs)
endif()

set_property(TARGET gocrispasr PROPERTY CXX_STANDARD 17)
set_target_properties(gocrispasr PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
