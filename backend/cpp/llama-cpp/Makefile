
LLAMA_VERSION?=e75ba4c0434eb759eb7ff74e034ebe729053e575
LLAMA_REPO?=https://github.com/ggerganov/llama.cpp

CMAKE_ARGS?=
BUILD_TYPE?=
ONEAPI_VARS?=/opt/intel/oneapi/setvars.sh
TARGET?=--target grpc-server

# Disable Shared libs as we are linking on static gRPC and we can't mix shared and static
CMAKE_ARGS+=-DBUILD_SHARED_LIBS=OFF -DLLAMA_CURL=OFF

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
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DCMAKE_CXX_FLAGS="-fsycl" \
		-DGGML_SYCL_F16=ON
endif

ifeq ($(BUILD_TYPE),sycl_f32)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DCMAKE_CXX_FLAGS="-fsycl"
endif

build-llama-cpp-grpc-server:
# Conditionally build grpc for the llama backend to use if needed
ifdef BUILD_GRPC_FOR_BACKEND_LLAMA
	$(MAKE) -C ../../grpc build
	_PROTOBUF_PROTOC=${INSTALLED_PACKAGES}/bin/proto \
	_GRPC_CPP_PLUGIN_EXECUTABLE=${INSTALLED_PACKAGES}/bin/grpc_cpp_plugin \
	PATH="${INSTALLED_PACKAGES}/bin:${PATH}" \
	CMAKE_ARGS="${CMAKE_ARGS} ${ADDED_CMAKE_ARGS}" \
	LLAMA_VERSION=$(CPPLLAMA_VERSION) \
	$(MAKE) grpc-server
else
	echo "BUILD_GRPC_FOR_BACKEND_LLAMA is not defined."
	LLAMA_VERSION=$(CPPLLAMA_VERSION) $(MAKE) grpc-server
endif


# This target is for manually building a variant with-auto detected flags
llama-cpp: llama.cpp purge
	$(info ${GREEN}I llama-cpp build info:avx2${RESET})
	$(MAKE) VARIANT="llama-cpp-copy" build-llama-cpp-grpc-server
	cp -rfv grpc-server llama-cpp

llama-cpp-avx2: backend-assets/grpc backend/cpp/llama-cpp/llama.cpp
	cp -rf backend/cpp/llama-cpp backend/cpp/llama-cpp-avx2
	$(MAKE) -C backend/cpp/llama-cpp-avx2 purge
	$(info ${GREEN}I llama-cpp build info:avx2${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) VARIANT="llama-avx2" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cpp-avx2/grpc-server backend-assets/grpc/llama-cpp-avx2

backend-assets/grpc/llama-cpp-avx512: backend-assets/grpc backend/cpp/llama-cpp/llama.cpp
	cp -rf backend/cpp/llama-cpp backend/cpp/llama-cpp-avx512
	$(MAKE) -C backend/cpp/llama-cpp-avx512 purge
	$(info ${GREEN}I llama-cpp build info:avx512${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) VARIANT="llama-avx512" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cpp-avx512/grpc-server backend-assets/grpc/llama-cpp-avx512

backend-assets/grpc/llama-cpp-avx: backend-assets/grpc backend/cpp/llama-cpp/llama.cpp
	cp -rf backend/cpp/llama-cpp backend/cpp/llama-cpp-avx
	$(MAKE) -C backend/cpp/llama-cpp-avx purge
	$(info ${GREEN}I llama-cpp build info:avx${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off" $(MAKE) VARIANT="llama-avx" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cpp-avx/grpc-server backend-assets/grpc/llama-cpp-avx

backend-assets/grpc/llama-cpp-fallback: backend-assets/grpc backend/cpp/llama-cpp/llama.cpp
	cp -rf backend/cpp/llama-cpp backend/cpp/llama-cpp-fallback
	$(MAKE) -C backend/cpp/llama-cpp-fallback purge
	$(info ${GREEN}I llama-cpp build info:fallback${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off" $(MAKE) VARIANT="llama-fallback" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cpp-fallback/grpc-server backend-assets/grpc/llama-cpp-fallback

backend-assets/grpc/llama-cpp-cuda: backend-assets/grpc backend/cpp/llama-cpp/llama.cpp
	cp -rf backend/cpp/llama-cpp backend/cpp/llama-cpp-cuda
	$(MAKE) -C backend/cpp/llama-cpp-cuda purge
	$(info ${GREEN}I llama-cpp build info:cuda${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_CUDA=ON" $(MAKE) VARIANT="llama-cuda" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cpp-cuda/grpc-server backend-assets/grpc/llama-cpp-cuda

backend-assets/grpc/llama-cpp-hipblas: backend-assets/grpc backend/cpp/llama-cpp/llama.cpp
	cp -rf backend/cpp/llama-cpp backend/cpp/llama-cpp-hipblas
	$(MAKE) -C backend/cpp/llama-cpp-hipblas purge
	$(info ${GREEN}I llama-cpp build info:hipblas${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off" BUILD_TYPE="hipblas" $(MAKE) VARIANT="llama-hipblas" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cpp-hipblas/grpc-server backend-assets/grpc/llama-cpp-hipblas

backend-assets/grpc/llama-cpp-sycl_f16: backend-assets/grpc backend/cpp/llama-cpp/llama.cpp
	cp -rf backend/cpp/llama-cpp backend/cpp/llama-cpp-sycl_f16
	$(MAKE) -C backend/cpp/llama-cpp-sycl_f16 purge
	$(info ${GREEN}I llama-cpp build info:sycl_f16${RESET})
	BUILD_TYPE="sycl_f16" $(MAKE) VARIANT="llama-sycl_f16" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cpp-sycl_f16/grpc-server backend-assets/grpc/llama-cpp-sycl_f16

backend-assets/grpc/llama-cpp-sycl_f32: backend-assets/grpc backend/cpp/llama-cpp/llama.cpp
	cp -rf backend/cpp/llama-cpp backend/cpp/llama-cpp-sycl_f32
	$(MAKE) -C backend/cpp/llama-cpp-sycl_f32 purge
	$(info ${GREEN}I llama-cpp build info:sycl_f32${RESET})
	BUILD_TYPE="sycl_f32" $(MAKE) VARIANT="llama-sycl_f32" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cpp-sycl_f32/grpc-server backend-assets/grpc/llama-cpp-sycl_f32

backend-assets/grpc/llama-cpp-grpc: backend-assets/grpc backend/cpp/llama-cpp/llama.cpp
	cp -rf backend/cpp/llama-cpp backend/cpp/llama-cpp-grpc
	$(MAKE) -C backend/cpp/llama-cpp-grpc purge
	$(info ${GREEN}I llama-cpp build info:grpc${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off" TARGET="--target grpc-server --target rpc-server" $(MAKE) VARIANT="llama-grpc" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cpp-grpc/grpc-server backend-assets/grpc/llama-cpp-grpc

backend-assets/util/llama-cpp-rpc-server: backend-assets/grpc/llama-cpp-grpc
	mkdir -p backend-assets/util/
	cp -rf backend/cpp/llama-cpp-grpc/llama.cpp/build/bin/rpc-server backend-assets/util/llama-cpp-rpc-server


llama.cpp:
	mkdir -p llama.cpp
	cd llama.cpp && \
	git init && \
	git remote add origin $(LLAMA_REPO)  && \
	git fetch origin && \
	git checkout -b build $(LLAMA_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

llama.cpp/tools/grpc-server: llama.cpp
	mkdir -p llama.cpp/tools/grpc-server
	bash prepare.sh

rebuild:
	bash prepare.sh
	rm -rf grpc-server
	$(MAKE) grpc-server

purge:
	rm -rf llama.cpp/build
	rm -rf llama.cpp/tools/grpc-server
	rm -rf grpc-server

clean: purge
	rm -rf llama.cpp

grpc-server: llama.cpp llama.cpp/tools/grpc-server
	@echo "Building grpc-server with $(BUILD_TYPE) build type and $(CMAKE_ARGS)"
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	+bash -c "source $(ONEAPI_VARS); \
	cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release $(TARGET)"
else
	+cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release $(TARGET)
endif
	cp llama.cpp/build/bin/grpc-server .
