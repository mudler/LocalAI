# SPDX-License-Identifier: MIT
cmake_minimum_required(VERSION 3.24)
project(localai_gemx LANGUAGES C CXX)
set(GEMX_SANITIZERS OFF CACHE BOOL "" FORCE)
set(BUILD_TESTING OFF CACHE BOOL "" FORCE)
set(GGML_METAL OFF CACHE BOOL "" FORCE)
add_subdirectory(sources/gem-x.cpp)
target_sources(gemx PRIVATE native/bridge.cpp)
