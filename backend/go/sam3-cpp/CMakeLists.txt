cmake_minimum_required(VERSION 3.14)
project(gosam3 LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

# Build ggml as static libraries to avoid runtime .so dependencies
set(BUILD_SHARED_LIBS OFF CACHE BOOL "Build static libraries" FORCE)

set(SAM3_BUILD_EXAMPLES OFF CACHE BOOL "Disable sam3.cpp examples" FORCE)
set(SAM3_BUILD_TESTS OFF CACHE BOOL "Disable sam3.cpp tests" FORCE)

add_subdirectory(./sources/sam3.cpp)

add_library(gosam3 MODULE gosam3.cpp)
target_link_libraries(gosam3 PRIVATE sam3 ggml)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(gosam3 PRIVATE stdc++fs)
endif()

target_include_directories(gosam3 PUBLIC
    sources/sam3.cpp
    sources/sam3.cpp/ggml/include
)

set_property(TARGET gosam3 PROPERTY CXX_STANDARD 14)
set_target_properties(gosam3 PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
