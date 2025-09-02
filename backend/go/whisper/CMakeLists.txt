cmake_minimum_required(VERSION 3.12)
project(gowhisper LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

add_subdirectory(./sources/whisper.cpp)

add_library(gowhisper MODULE gowhisper.cpp)
target_link_libraries(gowhisper PRIVATE whisper ggml)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(gosd PRIVATE stdc++fs)
endif()

set_property(TARGET gowhisper PROPERTY CXX_STANDARD 17)
set_target_properties(gowhisper PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
