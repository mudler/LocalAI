cmake_minimum_required(VERSION 3.25)
project(localai_meshremesh LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
find_package(Threads REQUIRED)
find_package(CGAL 5.5 REQUIRED)
find_package(nlohmann_json 3.11 REQUIRED)
set(EIGEN_BUILD_DOC OFF CACHE BOOL "" FORCE)
set(BUILD_TESTING OFF CACHE BOOL "" FORCE)
add_subdirectory(sources/eigen EXCLUDE_FROM_ALL)
set(VENDOR ${CMAKE_CURRENT_SOURCE_DIR}/native/vendor)
set(AR ${VENDOR}/third_party/autoremesher)
file(GLOB AR_SOURCES CONFIGURE_DEPENDS ${AR}/src/AutoRemesher/*.cpp ${AR}/isotropicremesher/*.cpp)
add_library(meshremesh SHARED native/bridge.cpp
  ${VENDOR}/mesh_export.cpp ${VENDOR}/print_remesh.cpp ${VENDOR}/quad_remesh.cpp
  ${VENDOR}/third_party/xatlas/xatlas.cpp
  ${VENDOR}/third_party/tinybvh/tiny_bvh_impl.cpp
  ${VENDOR}/third_party/meshoptimizer/allocator.cpp
  ${VENDOR}/third_party/meshoptimizer/indexgenerator.cpp
  ${VENDOR}/third_party/meshoptimizer/simplifier.cpp
  ${VENDOR}/third_party/meshoptimizer/vfetchoptimizer.cpp
  ${AR}/t2_ar_log.cpp ${AR_SOURCES})
target_include_directories(meshremesh PRIVATE native ${VENDOR}/examples ${VENDOR}/stb
  ${VENDOR}/third_party/xatlas ${VENDOR}/third_party/tinybvh ${VENDOR}/third_party/meshoptimizer
  ${AR}/include ${AR}/isotropicremesher ${AR}/compat)
target_compile_definitions(meshremesh PRIVATE TRELLIS2_USE_CGAL=1 TRELLIS2_USE_AUTOREMESHER=1 TINYBVH_NO_SIMD _USE_MATH_DEFINES NOMINMAX)
target_link_libraries(meshremesh PRIVATE CGAL::CGAL Eigen3::Eigen Threads::Threads nlohmann_json::nlohmann_json)
