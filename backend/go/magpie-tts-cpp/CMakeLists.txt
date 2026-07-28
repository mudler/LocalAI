cmake_minimum_required(VERSION 3.16)
project(gomagpiettscpp LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

set(MAGPIE_DIR ${CMAKE_CURRENT_SOURCE_DIR}/sources/magpie-tts.cpp)

# Override upstream's CMAKE_CUDA_ARCHITECTURES before add_subdirectory.
if(NOT DEFINED CMAKE_CUDA_ARCHITECTURES)
    set(CMAKE_CUDA_ARCHITECTURES "75-virtual;80-virtual;86-real;89-real")
endif()

# The magpie-tts C-API is exported directly by the upstream shared library
# (magpie_tts_capi_* in libmagpie-tts.so, ggml statically linked inside), so
# unlike qwen3-tts-cpp / moss-tts-cpp no local C shim is needed -- this wrapper
# only configures the upstream project as a purego-loadable shared library.
set(MAGPIE_SHARED       ON  CACHE BOOL "" FORCE)
set(MAGPIE_BUILD_CLI    OFF CACHE BOOL "" FORCE)
set(MAGPIE_BUILD_TESTS  OFF CACHE BOOL "" FORCE)

# Upstream FORCE-overwrites GGML_CUDA / GGML_METAL / GGML_VULKAN / GGML_HIP from
# its own MAGPIE_GGML_* toggles, which would silently discard the -DGGML_*=ON
# flags the LocalAI Makefile passes for GPU BUILD_TYPEs. Translate them into the
# MAGPIE_GGML_* vocabulary before add_subdirectory. (GGML_SYCL / GGML_BLAS and
# the CPU ISA flags are not touched by upstream and reach ggml unchanged.)
foreach(_be CUDA METAL VULKAN HIP)
    if(GGML_${_be})
        set(MAGPIE_GGML_${_be} ON CACHE BOOL "" FORCE)
    endif()
endforeach()

add_subdirectory(${MAGPIE_DIR} magpie EXCLUDE_FROM_ALL)

# Place libmagpie-tts.so at the build root, where the Makefile picks it up.
set_target_properties(magpie-tts PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
