
LLAMA_VERSION?=
LLAMA_REPO?=https://github.com/ggerganov/llama.cpp

CMAKE_ARGS?=
BUILD_TYPE?=
ONEAPI_VARS?=/opt/intel/oneapi/setvars.sh
TARGET?=--target grpc-server

# Disable Shared libs as we are linking on static gRPC and we can't mix shared and static
CMAKE_ARGS+=-DBUILD_SHARED_LIBS=OFF

# If build type is cublas, then we set -DGGML_CUDA=ON to CMAKE_ARGS automatically
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON
# If build type is openblas then we set -DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
# to CMAKE_ARGS automatically
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
# If build type is clblas (openCL) we set -DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
else ifeq ($(BUILD_TYPE),clblas)
	CMAKE_ARGS+=-DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
# If it's hipblas we do have also to set CC=/opt/rocm/llvm/bin/clang CXX=/opt/rocm/llvm/bin/clang++ 
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DGGML_HIP=ON
# If it's OSX, DO NOT embed the metal library - -DGGML_METAL_EMBED_LIBRARY=ON requires further investigation
# But if it's OSX without metal, disable it here
else ifeq ($(OS),Darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
		TARGET+=--target ggml-metal
	endif
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	CMAKE_ARGS+=-DGGML_SYCL=ON -DCMAKE_C_COMPILER=icx -DCMAKE_CXX_COMPILER=icpx -DGGML_SYCL_F16=ON
endif

ifeq ($(BUILD_TYPE),sycl_f32)
	CMAKE_ARGS+=-DGGML_SYCL=ON -DCMAKE_C_COMPILER=icx -DCMAKE_CXX_COMPILER=icpx
endif

llama.cpp:
	mkdir -p llama.cpp
	cd llama.cpp && \
	git init && \
	git remote add origin $(LLAMA_REPO)  && \
	git fetch origin && \
	git checkout -b build $(LLAMA_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

llama.cpp/examples/grpc-server: llama.cpp
	mkdir -p llama.cpp/examples/grpc-server
	bash prepare.sh

rebuild:
	bash prepare.sh
	rm -rf grpc-server
	$(MAKE) grpc-server

purge:
	rm -rf llama.cpp/build
	rm -rf llama.cpp/examples/grpc-server
	rm -rf grpc-server

clean: purge
	rm -rf llama.cpp

grpc-server: llama.cpp llama.cpp/examples/grpc-server
	@echo "Building grpc-server with $(BUILD_TYPE) build type and $(CMAKE_ARGS)"
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	+bash -c "source $(ONEAPI_VARS); \
	cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release $(TARGET)"
else
	+cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release $(TARGET)
endif
	cp llama.cpp/build/bin/grpc-server .