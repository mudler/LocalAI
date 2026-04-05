
LLAMA_VERSION?=b8635075ffe27b135c49afb9a8b5c434bd42c502
LLAMA_REPO?=https://github.com/ggerganov/llama.cpp
BACKEND_NAME?=llama-cpp
SHARED_DIR?=$(CURDIR)
GRPC_SERVER_DIR?=tools/grpc-server
SERVER_SOURCE_DIR?=tools/server

CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false
ONEAPI_VARS?=/opt/intel/oneapi/setvars.sh
TARGET?=--target grpc-server
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)
ARCH?=$(shell uname -m)

# Disable Shared libs as we are linking on static gRPC and we can't mix shared and static
CMAKE_ARGS+=-DBUILD_SHARED_LIBS=OFF -DLLAMA_CURL=OFF

CURRENT_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF -DLLAMA_OPENSSL=OFF
endif
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
	ROCM_HOME ?= /opt/rocm
	ROCM_PATH ?= /opt/rocm
	export CXX=$(ROCM_HOME)/llvm/bin/clang++
	export CC=$(ROCM_HOME)/llvm/bin/clang
	AMDGPU_TARGETS?=gfx803,gfx900,gfx906,gfx908,gfx90a,gfx942,gfx1010,gfx1030,gfx1032,gfx1100,gfx1101,gfx1102,gfx1200,gfx1201
	CMAKE_ARGS+=-DGGML_HIP=ON -DAMDGPU_TARGETS=$(AMDGPU_TARGETS)
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=1
else ifeq ($(OS),Darwin)
	ifeq ($(BUILD_TYPE),)
		BUILD_TYPE=metal
	endif
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
		CMAKE_ARGS+=-DGGML_METAL_USE_BF16=ON
		CMAKE_ARGS+=-DGGML_OPENMP=OFF
	endif
	TARGET+=--target ggml-metal
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

# Variants to build for each architecture (can be overridden by forks)
X86_64_VARIANTS ?= llama-cpp-avx llama-cpp-avx2 llama-cpp-avx512 llama-cpp-fallback llama-cpp-grpc llama-cpp-rpc-server
ARM64_VARIANTS ?= llama-cpp-fallback llama-cpp-grpc llama-cpp-rpc-server

build-variants:
ifeq ($(ARCH),aarch64)
	@for v in $(ARM64_VARIANTS); do $(MAKE) $$v || exit 1; done
else
	@for v in $(X86_64_VARIANTS); do $(MAKE) $$v || exit 1; done
endif

INSTALLED_PACKAGES=$(CURDIR)/../grpc/installed_packages
INSTALLED_LIB_CMAKE=$(INSTALLED_PACKAGES)/lib/cmake
ADDED_CMAKE_ARGS=-Dabsl_DIR=${INSTALLED_LIB_CMAKE}/absl \
				 -DProtobuf_DIR=${INSTALLED_LIB_CMAKE}/protobuf \
				 -Dutf8_range_DIR=${INSTALLED_LIB_CMAKE}/utf8_range \
				 -DgRPC_DIR=${INSTALLED_LIB_CMAKE}/grpc \
				 -DCMAKE_CXX_STANDARD_INCLUDE_DIRECTORIES=${INSTALLED_PACKAGES}/include
build-llama-cpp-grpc-server:
# Conditionally build grpc for the llama backend to use if needed
ifdef BUILD_GRPC_FOR_BACKEND_LLAMA
	$(MAKE) -C ../../grpc build
	_PROTOBUF_PROTOC=${INSTALLED_PACKAGES}/bin/proto \
	_GRPC_CPP_PLUGIN_EXECUTABLE=${INSTALLED_PACKAGES}/bin/grpc_cpp_plugin \
	PATH="${INSTALLED_PACKAGES}/bin:${PATH}" \
	CMAKE_ARGS="${CMAKE_ARGS} ${ADDED_CMAKE_ARGS}" \
	LLAMA_VERSION=$(LLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(VARIANT) grpc-server
else
	echo "BUILD_GRPC_FOR_BACKEND_LLAMA is not defined."
	LLAMA_VERSION=$(LLAMA_VERSION) $(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(VARIANT) grpc-server
endif

llama-cpp-avx2: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME) $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-avx2-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-avx2-build purge
	$(info ${GREEN}I llama-cpp build info:avx2${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) VARIANT="$(BACKEND_NAME)-avx2-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-avx2-build/grpc-server llama-cpp-avx2

llama-cpp-avx512: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME) $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-avx512-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-avx512-build purge
	$(info ${GREEN}I llama-cpp build info:avx512${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) VARIANT="$(BACKEND_NAME)-avx512-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-avx512-build/grpc-server llama-cpp-avx512

llama-cpp-avx: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME) $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-avx-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-avx-build purge
	$(info ${GREEN}I llama-cpp build info:avx${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) VARIANT="$(BACKEND_NAME)-avx-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-avx-build/grpc-server llama-cpp-avx

llama-cpp-fallback: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME) $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-fallback-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-fallback-build purge
	$(info ${GREEN}I llama-cpp build info:fallback${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) VARIANT="$(BACKEND_NAME)-fallback-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-fallback-build/grpc-server llama-cpp-fallback

llama-cpp-grpc: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME) $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-grpc-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-grpc-build purge
	$(info ${GREEN}I llama-cpp build info:grpc${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" TARGET="--target grpc-server --target rpc-server" $(MAKE) VARIANT="$(BACKEND_NAME)-grpc-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-grpc-build/grpc-server llama-cpp-grpc

llama-cpp-rpc-server: llama-cpp-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../$(BACKEND_NAME)-grpc-build/llama.cpp/build/bin/rpc-server llama-cpp-rpc-server

llama.cpp:
	mkdir -p llama.cpp
	cd llama.cpp && \
	git init && \
	git remote add origin $(LLAMA_REPO)  && \
	git fetch origin && \
	(git checkout -b build $(LLAMA_VERSION) || git checkout -b build origin/$(LLAMA_VERSION)) && \
	git submodule update --init --recursive --depth 1 --single-branch

llama.cpp/$(GRPC_SERVER_DIR): llama.cpp
	mkdir -p llama.cpp/$(GRPC_SERVER_DIR)
	SHARED_DIR=$(SHARED_DIR) SERVER_SOURCE_DIR=$(SERVER_SOURCE_DIR) GRPC_SERVER_DIR=$(GRPC_SERVER_DIR) bash $(SHARED_DIR)/prepare.sh

rebuild:
	SHARED_DIR=$(SHARED_DIR) SERVER_SOURCE_DIR=$(SERVER_SOURCE_DIR) GRPC_SERVER_DIR=$(GRPC_SERVER_DIR) bash $(SHARED_DIR)/prepare.sh
	rm -rf grpc-server
	$(MAKE) grpc-server

package:
	bash $(SHARED_DIR)/package.sh

purge:
	rm -rf llama.cpp/build
	rm -rf llama.cpp/$(GRPC_SERVER_DIR)
	rm -rf grpc-server

clean: purge
	rm -rf llama.cpp

grpc-server: llama.cpp llama.cpp/$(GRPC_SERVER_DIR)
	@echo "Building grpc-server with $(BUILD_TYPE) build type and $(CMAKE_ARGS)"
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	+bash -c "source $(ONEAPI_VARS); \
	cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release -j $(JOBS) $(TARGET)"
else
	+cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release -j $(JOBS) $(TARGET)
endif
	cp llama.cpp/build/bin/grpc-server .
