cmake_minimum_required(VERSION 3.18)
project(gomosstts-cpp LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

set(MOSS_DIR ${CMAKE_CURRENT_SOURCE_DIR}/sources/moss-tts.cpp)

# Override upstream's CMAKE_CUDA_ARCHITECTURES before add_subdirectory.
if(NOT DEFINED CMAKE_CUDA_ARCHITECTURES)
    set(CMAKE_CUDA_ARCHITECTURES "75-virtual;80-virtual;86-real;89-real")
endif()

# Add the upstream project. Its own CMakeLists vendors ggml under
# third_party/ggml and builds the moss-tts library (STATIC by default, since we
# do NOT set MOSS_TTS_SHARED). EXCLUDE_FROM_ALL keeps its CLI tool / tests /
# examples from building unless referenced.
add_subdirectory(${MOSS_DIR} mosstts EXCLUDE_FROM_ALL)

add_library(gomosstts-cpp MODULE cpp/mossttscpp.cpp)
target_link_libraries(gomosstts-cpp PRIVATE moss-tts)

target_include_directories(gomosstts-cpp PRIVATE ${MOSS_DIR}/include ${MOSS_DIR}/src)
target_include_directories(gomosstts-cpp SYSTEM PRIVATE ${MOSS_DIR}/third_party/ggml/include)

# Link GPU backends if the upstream ggml created them. moss-tts links these
# PUBLIC when enabled, but we re-list them so the MODULE carries the symbols
# even under static ggml (mirrors qwen3-tts-cpp).
foreach(backend blas cuda hip metal vulkan sycl)
    if(TARGET ggml-${backend})
        target_link_libraries(gomosstts-cpp PRIVATE ggml-${backend})
        if(backend STREQUAL "cuda")
            find_package(CUDAToolkit QUIET)
            if(CUDAToolkit_FOUND)
                target_link_libraries(gomosstts-cpp PRIVATE CUDA::cudart)
            endif()
        endif()
    endif()
endforeach()

if(MSVC)
    target_compile_options(gomosstts-cpp PRIVATE /W4 /wd4100 /wd4505)
else()
    target_compile_options(gomosstts-cpp PRIVATE -Wall -Wextra
                          -Wno-unused-parameter -Wno-unused-function)
endif()

set_property(TARGET gomosstts-cpp PROPERTY CXX_STANDARD 17)
set_target_properties(gomosstts-cpp PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
