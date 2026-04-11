
IK_LLAMA_VERSION?=08ae48c667e3dcd3025821a8585190b4a46c2f7c
IK_LLAMA_REPO?=https://github.com/ikawrakow/ik_llama.cpp

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

INSTALLED_PACKAGES=$(CURDIR)/../grpc/installed_packages
INSTALLED_LIB_CMAKE=$(INSTALLED_PACKAGES)/lib/cmake
ADDED_CMAKE_ARGS=-Dabsl_DIR=${INSTALLED_LIB_CMAKE}/absl \
				 -DProtobuf_DIR=${INSTALLED_LIB_CMAKE}/protobuf \
				 -Dutf8_range_DIR=${INSTALLED_LIB_CMAKE}/utf8_range \
				 -DgRPC_DIR=${INSTALLED_LIB_CMAKE}/grpc \
				 -DCMAKE_CXX_STANDARD_INCLUDE_DIRECTORIES=${INSTALLED_PACKAGES}/include
build-ik-llama-cpp-grpc-server:
# Conditionally build grpc for the backend to use if needed
ifdef BUILD_GRPC_FOR_BACKEND_LLAMA
	$(MAKE) -C ../../grpc build
	_PROTOBUF_PROTOC=${INSTALLED_PACKAGES}/bin/proto \
	_GRPC_CPP_PLUGIN_EXECUTABLE=${INSTALLED_PACKAGES}/bin/grpc_cpp_plugin \
	PATH="${INSTALLED_PACKAGES}/bin:${PATH}" \
	CMAKE_ARGS="${CMAKE_ARGS} ${ADDED_CMAKE_ARGS}" \
	IK_LLAMA_VERSION=$(IK_LLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(VARIANT) grpc-server
else
	echo "BUILD_GRPC_FOR_BACKEND_LLAMA is not defined."
	IK_LLAMA_VERSION=$(IK_LLAMA_VERSION) $(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(VARIANT) grpc-server
endif

ik-llama-cpp-avx2: ik_llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-avx2-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-avx2-build purge
	$(info ${GREEN}I ik-llama-cpp build info:avx2${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) VARIANT="ik-llama-cpp-avx2-build" build-ik-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-avx2-build/grpc-server ik-llama-cpp-avx2

ik-llama-cpp-avx512: ik_llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-avx512-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-avx512-build purge
	$(info ${GREEN}I ik-llama-cpp build info:avx512${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) VARIANT="ik-llama-cpp-avx512-build" build-ik-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-avx512-build/grpc-server ik-llama-cpp-avx512

ik-llama-cpp-avx: ik_llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-avx-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-avx-build purge
	$(info ${GREEN}I ik-llama-cpp build info:avx${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) VARIANT="ik-llama-cpp-avx-build" build-ik-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-avx-build/grpc-server ik-llama-cpp-avx

ik-llama-cpp-fallback: ik_llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-fallback-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-fallback-build purge
	$(info ${GREEN}I ik-llama-cpp build info:fallback${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) VARIANT="ik-llama-cpp-fallback-build" build-ik-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-fallback-build/grpc-server ik-llama-cpp-fallback

ik-llama-cpp-grpc: ik_llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-grpc-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-grpc-build purge
	$(info ${GREEN}I ik-llama-cpp build info:grpc${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" TARGET="--target grpc-server --target rpc-server" $(MAKE) VARIANT="ik-llama-cpp-grpc-build" build-ik-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-grpc-build/grpc-server ik-llama-cpp-grpc

ik-llama-cpp-rpc-server: ik-llama-cpp-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../ik-llama-cpp-grpc-build/ik_llama.cpp/build/bin/rpc-server ik-llama-cpp-rpc-server

ik_llama.cpp:
	mkdir -p ik_llama.cpp
	cd ik_llama.cpp && \
	git init && \
	git remote add origin $(IK_LLAMA_REPO)  && \
	git fetch origin && \
	git checkout -b build $(IK_LLAMA_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

ik_llama.cpp/examples/grpc-server: ik_llama.cpp
	mkdir -p ik_llama.cpp/examples/grpc-server
	bash prepare.sh

rebuild:
	bash prepare.sh
	rm -rf grpc-server
	$(MAKE) grpc-server

package:
	bash package.sh

purge:
	rm -rf ik_llama.cpp/build
	rm -rf ik_llama.cpp/examples/grpc-server
	rm -rf grpc-server

clean: purge
	rm -rf ik_llama.cpp

grpc-server: ik_llama.cpp ik_llama.cpp/examples/grpc-server
	@echo "Building grpc-server with $(BUILD_TYPE) build type and $(CMAKE_ARGS)"
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	+bash -c "source $(ONEAPI_VARS); \
	cd ik_llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release -j $(JOBS) $(TARGET)"
else
	+cd ik_llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release -j $(JOBS) $(TARGET)
endif
	cp ik_llama.cpp/build/bin/grpc-server .
