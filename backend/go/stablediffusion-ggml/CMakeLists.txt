cmake_minimum_required(VERSION 3.12)
project(gosd LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

add_subdirectory(./sources/stablediffusion-ggml.cpp)

add_library(gosd MODULE gosd.cpp)
target_link_libraries(gosd PRIVATE stable-diffusion ggml)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(gosd PRIVATE stdc++fs)
endif()

target_include_directories(gosd PUBLIC
    stable-diffusion.cpp
    stable-diffusion.cpp/thirdparty
)

set_property(TARGET gosd PROPERTY CXX_STANDARD 17)
set_target_properties(gosd PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
