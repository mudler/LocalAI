cmake_minimum_required(VERSION 3.18)
project(librfdetrcpp LANGUAGES C CXX)

set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# Static-link ggml + rfdetr so the resulting .so has no runtime dependency on
# extra ggml/rfdetr shared libraries — only on libc/libstdc++/libgomp, which
# the LocalAI package step bundles into the docker image.
set(BUILD_SHARED_LIBS OFF CACHE BOOL "Build static libraries" FORCE)

# rfdetr.cpp build switches: skip CLI/tests, keep static lib.
set(RFDETR_BUILD_CLI OFF CACHE BOOL "Disable rfdetr CLI" FORCE)
set(RFDETR_BUILD_TESTS OFF CACHE BOOL "Disable rfdetr tests" FORCE)
set(RFDETR_SHARED OFF CACHE BOOL "Build rfdetr as static lib" FORCE)

# rt-detr.cpp's top-level CMakeLists invokes
# `bash ${CMAKE_SOURCE_DIR}/scripts/apply_ggml_patches.sh` to apply its
# in-tree ggml patches before descending into the submodule. When we
# `add_subdirectory` it from a parent project, `CMAKE_SOURCE_DIR` points
# at *our* directory, not theirs, so the script path resolves wrong.
#
# Run the patches script ourselves up front (it's idempotent — re-running
# is a no-op once patches are applied) so the rt-detr.cpp configure step
# is essentially a no-op for the patch hook.
set(RFDETR_CPP_SRC ${CMAKE_CURRENT_SOURCE_DIR}/sources/rt-detr.cpp)
if(EXISTS ${RFDETR_CPP_SRC}/scripts/apply_ggml_patches.sh)
    execute_process(
        COMMAND bash ${RFDETR_CPP_SRC}/scripts/apply_ggml_patches.sh
        RESULT_VARIABLE _rfdetr_patch_result
        OUTPUT_VARIABLE _rfdetr_patch_output
        ERROR_VARIABLE  _rfdetr_patch_error
        OUTPUT_STRIP_TRAILING_WHITESPACE
        ERROR_STRIP_TRAILING_WHITESPACE)
    if(NOT _rfdetr_patch_result EQUAL 0)
        message(FATAL_ERROR
            "Failed to apply ggml patches (exit ${_rfdetr_patch_result}):\n"
            "stdout:\n${_rfdetr_patch_output}\n"
            "stderr:\n${_rfdetr_patch_error}")
    endif()
    message(STATUS "${_rfdetr_patch_output}")
endif()

# Stage a shim 'scripts/apply_ggml_patches.sh' under our source dir so that
# rt-detr.cpp's CMakeLists — which calls
#   bash ${CMAKE_SOURCE_DIR}/scripts/apply_ggml_patches.sh
# — finds an idempotent no-op there. The real patches have already been
# applied above; this just satisfies the path lookup.
file(MAKE_DIRECTORY ${CMAKE_CURRENT_SOURCE_DIR}/scripts)
file(WRITE ${CMAKE_CURRENT_SOURCE_DIR}/scripts/apply_ggml_patches.sh
"#!/usr/bin/env bash
# Shim - patches were already applied by the parent CMakeLists.
exit 0
")
execute_process(COMMAND chmod +x ${CMAKE_CURRENT_SOURCE_DIR}/scripts/apply_ggml_patches.sh)

add_subdirectory(./sources/rt-detr.cpp)

# rfdetr.cpp's C-API symbols already live inside librfdetr (src/rfdetr_capi.cpp
# is compiled into the lib). We re-export them via a MODULE library that
# whole-archive-links rfdetr so the symbols are visible at dlopen time.
add_library(rfdetrcpp MODULE
    sources/rt-detr.cpp/src/rfdetr_capi.cpp)

target_include_directories(rfdetrcpp PRIVATE
    sources/rt-detr.cpp/include
    sources/rt-detr.cpp/src
    sources/rt-detr.cpp/third_party/stb
)

target_link_libraries(rfdetrcpp PRIVATE rfdetr ggml)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(rfdetrcpp PRIVATE stdc++fs)
endif()

set_property(TARGET rfdetrcpp PROPERTY CXX_STANDARD 17)
set_target_properties(rfdetrcpp PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
