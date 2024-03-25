
LLAMA_VERSION?=

CMAKE_ARGS?=
BUILD_TYPE?=
ONEAPI_VARS?=/opt/intel/oneapi/setvars.sh

# If build type is cublas, then we set -DLLAMA_CUBLAS=ON to CMAKE_ARGS automatically
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DLLAMA_CUBLAS=ON
# If build type is openblas then we set -DLLAMA_BLAS=ON -DLLAMA_BLAS_VENDOR=OpenBLAS
# to CMAKE_ARGS automatically
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DLLAMA_BLAS=ON -DLLAMA_BLAS_VENDOR=OpenBLAS
# If build type is clblas (openCL) we set -DLLAMA_CLBLAST=ON -DCLBlast_DIR=/some/path
else ifeq ($(BUILD_TYPE),clblas)
	CMAKE_ARGS+=-DLLAMA_CLBLAST=ON -DCLBlast_DIR=/some/path
# If it's hipblas we do have also to set CC=/opt/rocm/llvm/bin/clang CXX=/opt/rocm/llvm/bin/clang++ 
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DLLAMA_HIPBLAS=ON
# If it's OSX, DO NOT embed the metal library - -DLLAMA_METAL_EMBED_LIBRARY=ON requires further investigation
# But if it's OSX without metal, disable it here
else ifeq ($(OS),darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DLLAMA_METAL=OFF
	endif
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	CMAKE_ARGS+=-DLLAMA_SYCL=ON -DCMAKE_C_COMPILER=icx -DCMAKE_CXX_COMPILER=icpx -DLLAMA_SYCL_F16=ON
endif

ifeq ($(BUILD_TYPE),sycl_f32)
	CMAKE_ARGS+=-DLLAMA_SYCL=ON -DCMAKE_C_COMPILER=icx -DCMAKE_CXX_COMPILER=icpx
endif

llama.cpp:
	git clone --recurse-submodules https://github.com/ggerganov/llama.cpp llama.cpp
	if [ -z "$(LLAMA_VERSION)" ]; then \
		exit 1; \
	fi
	cd llama.cpp && git checkout -b build $(LLAMA_VERSION) && git submodule update --init --recursive --depth 1

llama.cpp/examples/grpc-server: llama.cpp
	mkdir -p llama.cpp/examples/grpc-server
	cp -r $(abspath ./)/CMakeLists.txt llama.cpp/examples/grpc-server/
	cp -r $(abspath ./)/grpc-server.cpp llama.cpp/examples/grpc-server/
	cp -rfv $(abspath ./)/json.hpp llama.cpp/examples/grpc-server/
	cp -rfv $(abspath ./)/utils.hpp llama.cpp/examples/grpc-server/
	echo "add_subdirectory(grpc-server)" >> llama.cpp/examples/CMakeLists.txt
## XXX: In some versions of CMake clip wasn't being built before llama.
## This is an hack for now, but it should be fixed in the future.
	cp -rfv llama.cpp/examples/llava/clip.h llama.cpp/examples/grpc-server/clip.h
	cp -rfv llama.cpp/examples/llava/llava.cpp llama.cpp/examples/grpc-server/llava.cpp
	echo '#include "llama.h"' > llama.cpp/examples/grpc-server/llava.h
	cat llama.cpp/examples/llava/llava.h >> llama.cpp/examples/grpc-server/llava.h
	cp -rfv llama.cpp/examples/llava/clip.cpp llama.cpp/examples/grpc-server/clip.cpp

rebuild:
	cp -rfv $(abspath ./)/CMakeLists.txt llama.cpp/examples/grpc-server/
	cp -rfv $(abspath ./)/grpc-server.cpp llama.cpp/examples/grpc-server/
	cp -rfv $(abspath ./)/json.hpp llama.cpp/examples/grpc-server/
	rm -rf grpc-server
	$(MAKE) grpc-server

clean:
	rm -rf llama.cpp
	rm -rf grpc-server

grpc-server: llama.cpp llama.cpp/examples/grpc-server
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	bash -c "source $(ONEAPI_VARS); \
	cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release"	
else
	cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release
endif
	cp llama.cpp/build/bin/grpc-server .