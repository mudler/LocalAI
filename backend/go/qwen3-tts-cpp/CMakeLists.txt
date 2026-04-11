cmake_minimum_required(VERSION 3.14)
project(goqwen3ttscpp LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

set(QWEN3TTS_DIR ${CMAKE_CURRENT_SOURCE_DIR}/sources/qwen3-tts.cpp)

# Override upstream's CMAKE_CUDA_ARCHITECTURES before add_subdirectory.
if(NOT DEFINED CMAKE_CUDA_ARCHITECTURES)
    set(CMAKE_CUDA_ARCHITECTURES "75-virtual;80-virtual;86-real;89-real")
endif()

# Build ggml from the upstream's submodule FIRST, so that ggml/ggml-base/ggml-cpu
# CMake targets exist when the upstream project references them by name.
# The upstream CMakeLists.txt uses target_link_libraries(... ggml ggml-base ggml-cpu)
# with target_link_directories pointing at a pre-built ggml/build/. By adding ggml
# as a subdirectory here, CMake resolves those names as targets instead.
add_subdirectory(${QWEN3TTS_DIR}/ggml ggml EXCLUDE_FROM_ALL)

# Now add the upstream project
add_subdirectory(${QWEN3TTS_DIR} qwen3tts EXCLUDE_FROM_ALL)

add_library(goqwen3ttscpp MODULE cpp/goqwen3ttscpp.cpp)
target_link_libraries(goqwen3ttscpp PRIVATE qwen3_tts)

target_include_directories(goqwen3ttscpp PRIVATE ${QWEN3TTS_DIR}/src)
target_include_directories(goqwen3ttscpp SYSTEM PRIVATE ${QWEN3TTS_DIR}/ggml/include)

# Link GPU backends if available
foreach(backend blas cuda metal vulkan)
    if(TARGET ggml-${backend})
        target_link_libraries(goqwen3ttscpp PRIVATE ggml-${backend})
        string(TOUPPER ${backend} BACKEND_UPPER)
        target_compile_definitions(goqwen3ttscpp PRIVATE QWEN3TTS_HAVE_${BACKEND_UPPER})
        if(backend STREQUAL "cuda")
            find_package(CUDAToolkit QUIET)
            if(CUDAToolkit_FOUND)
                target_link_libraries(goqwen3ttscpp PRIVATE CUDA::cudart)
            endif()
        endif()
    endif()
endforeach()

if(MSVC)
    target_compile_options(goqwen3ttscpp PRIVATE /W4 /wd4100 /wd4505)
else()
    target_compile_options(goqwen3ttscpp PRIVATE -Wall -Wextra -Wshadow -Wconversion
                          -Wno-unused-parameter -Wno-unused-function -Wno-sign-conversion)
endif()

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(goqwen3ttscpp PRIVATE stdc++fs)
endif()

set_property(TARGET goqwen3ttscpp PROPERTY CXX_STANDARD 17)
set_target_properties(goqwen3ttscpp PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
