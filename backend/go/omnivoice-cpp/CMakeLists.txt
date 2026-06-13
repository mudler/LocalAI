cmake_minimum_required(VERSION 3.14)
project(gomnivoicecpp LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

set(OMNIVOICE_DIR ${CMAKE_CURRENT_SOURCE_DIR}/sources/omnivoice.cpp)

# Override upstream's CMAKE_CUDA_ARCHITECTURES before add_subdirectory.
if(NOT DEFINED CMAKE_CUDA_ARCHITECTURES)
    set(CMAKE_CUDA_ARCHITECTURES "75-virtual;80-virtual;86-real;89-real")
endif()

# Add the upstream project. Its own CMakeLists adds ggml + builds
# omnivoice-core (STATIC, contains src/omnivoice.cpp i.e. the ov_* impl).
# EXCLUDE_FROM_ALL keeps its CLI tools/tests from building unless referenced.
add_subdirectory(${OMNIVOICE_DIR} omnivoice EXCLUDE_FROM_ALL)

# Upstream generates version.h into its own CMAKE_CURRENT_BINARY_DIR and adds
# the top-level ${CMAKE_BINARY_DIR} to omnivoice-core's include path. When the
# project is nested under add_subdirectory those two directories differ
# (<build>/omnivoice vs <build>), so omnivoice.cpp cannot find version.h. Point
# omnivoice-core at the subproject binary dir where version.h is actually
# generated. (Fix lives here, never in the fetched upstream checkout.)
target_include_directories(omnivoice-core PRIVATE ${CMAKE_BINARY_DIR}/omnivoice)

add_library(gomnivoicecpp MODULE cpp/gomnivoicecpp.cpp)
target_link_libraries(gomnivoicecpp PRIVATE omnivoice-core)

target_include_directories(gomnivoicecpp PRIVATE ${OMNIVOICE_DIR}/src)
target_include_directories(gomnivoicecpp SYSTEM PRIVATE ${OMNIVOICE_DIR}/ggml/include)

# Link GPU backends if the upstream ggml created them.
foreach(backend blas cuda metal vulkan sycl)
    if(TARGET ggml-${backend})
        target_link_libraries(gomnivoicecpp PRIVATE ggml-${backend})
        if(backend STREQUAL "cuda")
            find_package(CUDAToolkit QUIET)
            if(CUDAToolkit_FOUND)
                target_link_libraries(gomnivoicecpp PRIVATE CUDA::cudart)
            endif()
        endif()
    endif()
endforeach()

if(MSVC)
    target_compile_options(gomnivoicecpp PRIVATE /W4 /wd4100 /wd4505)
else()
    target_compile_options(gomnivoicecpp PRIVATE -Wall -Wextra
                          -Wno-unused-parameter -Wno-unused-function)
endif()

set_property(TARGET gomnivoicecpp PROPERTY CXX_STANDARD 17)
set_target_properties(gomnivoicecpp PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
