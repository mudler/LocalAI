cmake_minimum_required(VERSION 3.18)
project(liblocateanythingcpp LANGUAGES C CXX)

set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# Static-link ggml + locate_anything so the resulting .so has no runtime
# dependency on extra ggml/locate_anything shared libraries — only on
# libc/libstdc++/libgomp, which the LocalAI package step bundles into the
# docker image.
set(BUILD_SHARED_LIBS OFF CACHE BOOL "Build static libraries" FORCE)

# locate-anything.cpp build switches: skip CLI/tests, keep static lib.
set(LA_BUILD_CLI OFF CACHE BOOL "Disable locate-anything CLI" FORCE)
set(LA_BUILD_TESTS OFF CACHE BOOL "Disable locate-anything tests" FORCE)
set(LA_SHARED OFF CACHE BOOL "Build locate_anything as static lib" FORCE)

# Unlike rt-detr.cpp, locate-anything.cpp ships no in-tree ggml patches, so
# there is no apply_ggml_patches.sh hook to shim here.
add_subdirectory(./sources/locate-anything.cpp)

# locate-anything.cpp's top-level CMakeLists points its own target's include
# dirs at ${CMAKE_SOURCE_DIR}/{include,src,third_party,...}. CMAKE_SOURCE_DIR
# is the *top-level* source dir of the whole CMake tree, so when we pull it in
# via add_subdirectory it resolves to OUR directory, not theirs, and the
# locate_anything target fails to find its own headers (la_capi.h, stb_image.h,
# la_gguf_keys.h). Re-add the correct, subdir-relative include paths to the
# already-defined target so it compiles regardless of where it's nested.
set(LA_SRC ${CMAKE_CURRENT_SOURCE_DIR}/sources/locate-anything.cpp)
target_include_directories(locate_anything PRIVATE
    ${LA_SRC}/include
    ${LA_SRC}/src
    ${LA_SRC}/third_party
    ${LA_SRC}/third_party/stb)

# locate-anything.cpp's C-API symbols already live inside liblocate_anything
# (src/la_capi.cpp is compiled into the lib). We re-export them via a MODULE
# library that links locate_anything so the symbols are visible at dlopen time.
add_library(locateanythingcpp MODULE
    sources/locate-anything.cpp/src/la_capi.cpp)

target_include_directories(locateanythingcpp PRIVATE
    sources/locate-anything.cpp/include
    sources/locate-anything.cpp/src
    sources/locate-anything.cpp/third_party
    sources/locate-anything.cpp/third_party/stb
)

target_link_libraries(locateanythingcpp PRIVATE locate_anything ggml)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(locateanythingcpp PRIVATE stdc++fs)
endif()

set_property(TARGET locateanythingcpp PROPERTY CXX_STANDARD 17)
set_target_properties(locateanythingcpp PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
