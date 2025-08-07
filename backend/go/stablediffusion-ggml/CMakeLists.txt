cmake_minimum_required(VERSION 3.12)
project(gosd LANGUAGES C CXX)

add_subdirectory(./sources/stablediffusion-ggml.cpp)

add_library(gosd MODULE gosd.cpp)
target_link_libraries(gosd PRIVATE stable-diffusion ggml zip stdc++fs)

target_include_directories(gosd PUBLIC
    stable-diffusion.cpp
    stable-diffusion.cpp/thirdparty
)

set_property(TARGET gosd PROPERTY CXX_STANDARD 17)
set_target_properties(gosd PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
