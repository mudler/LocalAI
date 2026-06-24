cmake_minimum_required(VERSION 3.18)
project(libdepthanythingcpp LANGUAGES C CXX)

set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# Static-link ggml into the depth-anything shared library so the resulting .so
# has no runtime dependency on an external libggml — only on
# libc/libstdc++/libgomp, which the LocalAI package step bundles into the
# docker image.
set(BUILD_SHARED_LIBS OFF CACHE BOOL "Build static libraries" FORCE)

# depth-anything.cpp build switches: skip CLI/tests, but build libdepthanything
# itself as a SHARED library (DA_SHARED) while ggml stays static
# (BUILD_SHARED_LIBS OFF above). The da_capi_* C ABI is compiled into
# src/da_capi.cpp and re-exported by that shared library, so no extra MODULE
# wrapper is needed (unlike locate-anything.cpp).
set(DA_BUILD_CLI OFF CACHE BOOL "Disable depth-anything CLI" FORCE)
set(DA_BUILD_TESTS OFF CACHE BOOL "Disable depth-anything tests" FORCE)
set(DA_SHARED ON CACHE BOOL "Build libdepthanything as a shared lib" FORCE)

add_subdirectory(./sources/depth-anything.cpp)

# Emit libdepthanything.so into the top-level build dir so the Makefile can
# rename it to the per-variant libdepthanythingcpp-<variant>.so.
set_target_properties(depthanything PROPERTIES
    LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
