cmake_minimum_required(VERSION 3.14)
project(goqwen3ttscpp LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

set(QWENTTS_DIR ${CMAKE_CURRENT_SOURCE_DIR}/sources/qwentts.cpp)

# Override upstream's CMAKE_CUDA_ARCHITECTURES before add_subdirectory.
if(NOT DEFINED CMAKE_CUDA_ARCHITECTURES)
    set(CMAKE_CUDA_ARCHITECTURES "75-virtual;80-virtual;86-real;89-real")
endif()

# Add the upstream project. Its own CMakeLists adds ggml + cpp-httplib + yyjson
# and builds qwen-core (STATIC, the qt_* impl). EXCLUDE_FROM_ALL keeps its CLI
# tools / tts-server / tests from building unless referenced.
add_subdirectory(${QWENTTS_DIR} qwentts EXCLUDE_FROM_ALL)

# Upstream generates version.h into its own CMAKE_CURRENT_BINARY_DIR and adds
# the top-level ${CMAKE_BINARY_DIR} to qwen-core's include path. Under
# add_subdirectory those two dirs differ (<build>/qwentts vs <build>), so
# qwen.cpp cannot find version.h. Point qwen-core at the subproject binary dir
# where version.h is actually generated. (Fix lives here, never in the fetched
# upstream checkout.)
target_include_directories(qwen-core PRIVATE ${CMAKE_BINARY_DIR}/qwentts)

add_library(goqwen3ttscpp MODULE cpp/goqwen3ttscpp.cpp)
target_link_libraries(goqwen3ttscpp PRIVATE qwen-core)

target_include_directories(goqwen3ttscpp PRIVATE ${QWENTTS_DIR}/src)
target_include_directories(goqwen3ttscpp SYSTEM PRIVATE ${QWENTTS_DIR}/ggml/include)

# Link GPU backends if the upstream ggml created them.
foreach(backend blas cuda metal vulkan sycl)
    if(TARGET ggml-${backend})
        target_link_libraries(goqwen3ttscpp PRIVATE ggml-${backend})
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
    target_compile_options(goqwen3ttscpp PRIVATE -Wall -Wextra
                          -Wno-unused-parameter -Wno-unused-function)
endif()

set_property(TARGET goqwen3ttscpp PROPERTY CXX_STANDARD 17)
set_target_properties(goqwen3ttscpp PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
