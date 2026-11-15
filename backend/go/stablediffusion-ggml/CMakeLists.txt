cmake_minimum_required(VERSION 3.12)
project(gosd LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

# stable-diffusion.cpp compiles its CUDA extension kernels (src/core/cuda) and
# its CUTLASS sources (CuTe flash attention, BF16 and INT8 GEMM) outside
# ggml-cuda. ggml sets its default architecture list in its own directory
# scope, so that list does not reach these targets. Without a value they are
# compiled only for the nvcc default architecture (sm_75 on CUDA 13). Their
# SM80 code paths then compile to a breakpoint, but the runtime checks look at
# the device (compute capability 8 or higher), so on Ampere and newer GPUs the
# breakpoint runs and the kernel fails with "unspecified launch failure".
# Use the ggml default list (ggml-cuda/CMakeLists.txt) for the whole tree, so
# ggml-cuda and these targets get the same code. Jetson Orin (sm_87) has no
# real entry, as in ggml: it runs the PTX of 80-virtual. The list is used also
# when GGML_NATIVE is on, because ggml's "native" choice cannot reach these
# targets either. An explicit -DCMAKE_CUDA_ARCHITECTURES still wins.
if(SD_CUDA AND NOT DEFINED CMAKE_CUDA_ARCHITECTURES)
    find_package(CUDAToolkit REQUIRED)
    set(CMAKE_CUDA_ARCHITECTURES "")
    if(CUDAToolkit_VERSION VERSION_LESS "13")
        list(APPEND CMAKE_CUDA_ARCHITECTURES 50-virtual 61-virtual 70-virtual)
    endif()
    list(APPEND CMAKE_CUDA_ARCHITECTURES 75-virtual 80-virtual 86-real)
    if(CUDAToolkit_VERSION VERSION_GREATER_EQUAL "11.8")
        list(APPEND CMAKE_CUDA_ARCHITECTURES 89-real 90-virtual)
    endif()
    if(CUDAToolkit_VERSION VERSION_GREATER_EQUAL "12.8")
        list(APPEND CMAKE_CUDA_ARCHITECTURES 120a-real)
    endif()
    if(CUDAToolkit_VERSION VERSION_GREATER_EQUAL "12.9")
        list(APPEND CMAKE_CUDA_ARCHITECTURES 121a-real)
    endif()
    message(STATUS "gosd: CUDA architectures ${CMAKE_CUDA_ARCHITECTURES}")
endif()

add_subdirectory(./sources/stablediffusion-ggml.cpp)

add_library(gosd MODULE cpp/gosd.cpp)
target_link_libraries(gosd PRIVATE stable-diffusion ggml)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(gosd PRIVATE stdc++fs)
endif()

target_include_directories(gosd PUBLIC
    sources/stablediffusion-ggml.cpp/include
    sources/stablediffusion-ggml.cpp/thirdparty
)

set_property(TARGET gosd PROPERTY CXX_STANDARD 17)
set_target_properties(gosd PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
