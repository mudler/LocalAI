cmake_minimum_required(VERSION 3.18)
project(govibevoicecpp LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

set(VIBEVOICE_DIR ${CMAKE_CURRENT_SOURCE_DIR}/sources/vibevoice.cpp)

# Override upstream's CMAKE_CUDA_ARCHITECTURES before add_subdirectory.
if(NOT DEFINED CMAKE_CUDA_ARCHITECTURES)
    set(CMAKE_CUDA_ARCHITECTURES "75-virtual;80-virtual;86-real;89-real")
endif()

# Force-disable upstream tests/examples — we only need libvibevoice.
set(VIBEVOICE_BUILD_TESTS    OFF CACHE BOOL "" FORCE)
set(VIBEVOICE_BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(VIBEVOICE_BUILD_SERVER   OFF CACHE BOOL "" FORCE)

# vibevoice.cpp's top-level CMakeLists already adds third_party/ggml as a
# subdirectory — no need to add it explicitly here, just include the
# whole project.
add_subdirectory(${VIBEVOICE_DIR} vibevoice EXCLUDE_FROM_ALL)

add_library(govibevoicecpp MODULE cpp/govibevoicecpp.cpp)

# libvibevoice is STATIC; without --whole-archive the linker GCs the
# vv_capi_* symbols (purego dlopens them by name, nothing in our
# translation unit references them). Force the static archive's
# entire contents into the MODULE so dlsym finds vv_capi_load etc.
if(APPLE)
    target_link_libraries(govibevoicecpp PRIVATE -Wl,-force_load $<TARGET_FILE:vibevoice>)
elseif(MSVC)
    target_link_libraries(govibevoicecpp PRIVATE vibevoice)
    set_property(TARGET govibevoicecpp APPEND PROPERTY LINK_FLAGS "/WHOLEARCHIVE:vibevoice")
else()
    target_link_libraries(govibevoicecpp PRIVATE
        -Wl,--whole-archive vibevoice -Wl,--no-whole-archive)
endif()

target_include_directories(govibevoicecpp PRIVATE ${VIBEVOICE_DIR}/include)
target_include_directories(govibevoicecpp SYSTEM PRIVATE ${VIBEVOICE_DIR}/third_party/ggml/include)

# Link GPU backends if available — vibevoice's own CMake already links
# these to the libvibevoice STATIC library, but we re-link them on the
# MODULE so resolved symbols include all backend kernels.
foreach(backend blas cuda metal vulkan)
    if(TARGET ggml-${backend})
        target_link_libraries(govibevoicecpp PRIVATE ggml-${backend})
        string(TOUPPER ${backend} BACKEND_UPPER)
        target_compile_definitions(govibevoicecpp PRIVATE VIBEVOICE_HAVE_${BACKEND_UPPER})
        if(backend STREQUAL "cuda")
            find_package(CUDAToolkit QUIET)
            if(CUDAToolkit_FOUND)
                target_link_libraries(govibevoicecpp PRIVATE CUDA::cudart)
            endif()
        endif()
    endif()
endforeach()

if(MSVC)
    target_compile_options(govibevoicecpp PRIVATE /W4 /wd4100 /wd4505)
else()
    target_compile_options(govibevoicecpp PRIVATE -Wall -Wextra -Wshadow
                          -Wno-unused-parameter -Wno-unused-function -Wno-sign-conversion)
endif()

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(govibevoicecpp PRIVATE stdc++fs)
endif()

set_property(TARGET govibevoicecpp PROPERTY CXX_STANDARD 17)
set_target_properties(govibevoicecpp PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
