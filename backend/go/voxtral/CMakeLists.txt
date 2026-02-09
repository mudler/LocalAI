cmake_minimum_required(VERSION 3.12)

if(USE_METAL)
    project(govoxtral LANGUAGES C OBJC)
else()
    project(govoxtral LANGUAGES C)
endif()

set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

# Workaround: CMake + GCC linker depfile generation fails for MODULE libraries
set(CMAKE_C_LINKER_DEPFILE_SUPPORTED FALSE)

# Build voxtral.c as a library
set(VOXTRAL_SOURCES
    sources/voxtral.c/voxtral.c
    sources/voxtral.c/voxtral_kernels.c
    sources/voxtral.c/voxtral_audio.c
    sources/voxtral.c/voxtral_encoder.c
    sources/voxtral.c/voxtral_decoder.c
    sources/voxtral.c/voxtral_tokenizer.c
    sources/voxtral.c/voxtral_safetensors.c
)

# Metal GPU acceleration (macOS arm64 only)
if(USE_METAL)
    # Generate embedded shader header from .metal source via xxd
    add_custom_command(
        OUTPUT ${CMAKE_CURRENT_SOURCE_DIR}/sources/voxtral.c/voxtral_shaders_source.h
        COMMAND xxd -i voxtral_shaders.metal > voxtral_shaders_source.h
        WORKING_DIRECTORY ${CMAKE_CURRENT_SOURCE_DIR}/sources/voxtral.c
        DEPENDS sources/voxtral.c/voxtral_shaders.metal
        COMMENT "Generating embedded Metal shaders header"
    )
    list(APPEND VOXTRAL_SOURCES sources/voxtral.c/voxtral_metal.m)
    set_source_files_properties(sources/voxtral.c/voxtral_metal.m PROPERTIES
        COMPILE_FLAGS "-fobjc-arc"
    )
endif()

add_library(govoxtral MODULE csrc/govoxtral.c ${VOXTRAL_SOURCES})

target_include_directories(govoxtral PRIVATE sources/voxtral.c csrc)

target_compile_options(govoxtral PRIVATE -O3 -ffast-math)

if(USE_METAL)
    target_compile_definitions(govoxtral PRIVATE USE_BLAS USE_METAL ACCELERATE_NEW_LAPACK)
    target_link_libraries(govoxtral PRIVATE
        "-framework Accelerate"
        "-framework Metal"
        "-framework MetalPerformanceShaders"
        "-framework MetalPerformanceShadersGraph"
        "-framework Foundation"
        "-framework AudioToolbox"
        "-framework CoreFoundation"
        m
    )
    # Ensure the generated shader header is built before compiling
    target_sources(govoxtral PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/sources/voxtral.c/voxtral_shaders_source.h
    )
elseif(USE_OPENBLAS)
    # Try to find OpenBLAS; use it if available, otherwise fall back to pure C
    find_package(BLAS)
    if(BLAS_FOUND)
        target_compile_definitions(govoxtral PRIVATE USE_BLAS USE_OPENBLAS)
        target_link_libraries(govoxtral PRIVATE ${BLAS_LIBRARIES} m)
        target_include_directories(govoxtral PRIVATE /usr/include/openblas)
    else()
        message(WARNING "OpenBLAS requested but not found, building without BLAS")
        target_link_libraries(govoxtral PRIVATE m)
    endif()
elseif(APPLE)
    # macOS without Metal: use Accelerate framework
    target_compile_definitions(govoxtral PRIVATE USE_BLAS ACCELERATE_NEW_LAPACK)
    target_link_libraries(govoxtral PRIVATE "-framework Accelerate" m)
else()
    target_link_libraries(govoxtral PRIVATE m)
endif()

set_property(TARGET govoxtral PROPERTY C_STANDARD 11)
set_target_properties(govoxtral PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
