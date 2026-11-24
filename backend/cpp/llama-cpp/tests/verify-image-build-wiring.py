# SPDX-License-Identifier: MIT
"""Exercise the production CMake decoder dependency block, including old forks."""
import pathlib
import subprocess
import tempfile

backend = pathlib.Path(__file__).resolve().parents[1]
cmake = (backend / 'CMakeLists.txt').read_text()
start = cmake.index('if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/../server/server-decision.cpp")')
block = cmake[start:cmake.index('endif()', start) + len('endif()')]
with tempfile.TemporaryDirectory() as tmp:
    root = pathlib.Path(tmp)
    source = root / 'grpc-server'
    source.mkdir()
    (root / 'server').mkdir()
    (source / 'main.cpp').write_text('int main() {}\n')
    (source / 'CMakeLists.txt').write_text('''cmake_minimum_required(VERSION 3.15)
project(decoder_wiring LANGUAGES CXX)
set(TARGET grpc-server)
add_executable(${TARGET} main.cpp)
''' + block + '''
get_target_property(libs ${TARGET} LINK_LIBRARIES)
if(EXPECT_DECODERS)
    if(NOT "${libs}" STREQUAL "ZLIB::ZLIB;JPEG::JPEG")
        message(FATAL_ERROR "Missing decoder links: ${libs}")
    endif()
elseif(libs)
    message(FATAL_ERROR "Old fork acquired decoder dependencies: ${libs}")
endif()
''')
    subprocess.run(['cmake', '-S', str(source), '-B', str(root / 'fork'),
                    '-DCMAKE_DISABLE_FIND_PACKAGE_ZLIB=TRUE',
                    '-DCMAKE_DISABLE_FIND_PACKAGE_JPEG=TRUE'], check=True)
    (root / 'server/server-decision.cpp').touch()
    subprocess.run(['cmake', '-S', str(source), '-B', str(root / 'native'),
                    '-DEXPECT_DECODERS=ON'], check=True)
    subprocess.run(['cmake', '--build', str(root / 'native')], check=True)
print('Production CMake decoder links and old-fork guard PASS')
