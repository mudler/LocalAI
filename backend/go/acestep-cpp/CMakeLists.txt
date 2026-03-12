cmake_minimum_required(VERSION 3.14)
project(goacestepcpp LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

set(ACESTEP_DIR ${CMAKE_CURRENT_SOURCE_DIR}/sources/acestep.cpp)

# Override upstream's CMAKE_CUDA_ARCHITECTURES before add_subdirectory.
# Upstream sets 120a/121a for CUDA >= 12.8, but those archs require a newer
# toolkit than 12.8.x ships. Pre-defining this variable makes the upstream
# "if(NOT DEFINED CMAKE_CUDA_ARCHITECTURES)" guard skip its broken defaults.
if(NOT DEFINED CMAKE_CUDA_ARCHITECTURES)
    set(CMAKE_CUDA_ARCHITECTURES "75-virtual;80-virtual;86-real;89-real")
endif()

# EXCLUDE_FROM_ALL: only build targets we explicitly depend on (acestep-core, ggml),
# skip upstream standalone executables (ace-understand, dit-vae, etc.)
add_subdirectory(${ACESTEP_DIR} acestep EXCLUDE_FROM_ALL)

add_library(goacestepcpp MODULE cpp/goacestepcpp.cpp)
target_link_libraries(goacestepcpp PRIVATE acestep-core ggml ggml-base ggml-cpu)

# Include dirs matching link_ggml_backends macro, but with absolute paths
target_include_directories(goacestepcpp PRIVATE ${ACESTEP_DIR}/src ${ACESTEP_DIR})
target_include_directories(goacestepcpp SYSTEM PRIVATE ${ACESTEP_DIR}/ggml/include)

# Link GPU backends if available (mirrors link_ggml_backends macro)
foreach(backend blas cuda metal vulkan)
    if(TARGET ggml-${backend})
        target_link_libraries(goacestepcpp PRIVATE ggml-${backend})
        string(TOUPPER ${backend} BACKEND_UPPER)
        target_compile_definitions(goacestepcpp PRIVATE ACESTEP_HAVE_${BACKEND_UPPER})
        if(backend STREQUAL "cuda")
            find_package(CUDAToolkit QUIET)
            if(CUDAToolkit_FOUND)
                target_link_libraries(goacestepcpp PRIVATE CUDA::cudart)
            endif()
        endif()
    endif()
endforeach()

if(MSVC)
    target_compile_options(goacestepcpp PRIVATE /W4 /wd4100 /wd4505)
else()
    target_compile_options(goacestepcpp PRIVATE -Wall -Wextra -Wshadow -Wconversion
                          -Wno-unused-parameter -Wno-unused-function -Wno-sign-conversion)
endif()

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(goacestepcpp PRIVATE stdc++fs)
endif()

set_property(TARGET goacestepcpp PROPERTY CXX_STANDARD 17)
set_target_properties(goacestepcpp PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
