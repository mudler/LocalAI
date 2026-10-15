cmake_minimum_required(VERSION 3.25)
project(localai_kimodo LANGUAGES CXX)

set(BUILD_SHARED_LIBS ON CACHE BOOL "" FORCE)
set(KIMODO_BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(BUILD_TESTING OFF CACHE BOOL "" FORCE)
add_subdirectory(sources/kimodo.cpp kimodo)
target_sources(kimodo PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/native/bridge.cpp)
