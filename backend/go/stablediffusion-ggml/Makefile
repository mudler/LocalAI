CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# stablediffusion.cpp (ggml)
STABLEDIFFUSION_GGML_REPO?=https://github.com/leejet/stable-diffusion.cpp
STABLEDIFFUSION_GGML_VERSION?=e411520407663e1ddf8ff2e5ed4ff3a116fbbc97

CMAKE_ARGS+=-DGGML_MAX_NAME=128

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# If build type is cublas, then we set -DGGML_CUDA=ON to CMAKE_ARGS automatically
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DSD_CUDA=ON -DGGML_CUDA=ON
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
	CMAKE_ARGS+=-DSD_HIPBLAS=ON -DGGML_HIPBLAS=ON -DAMDGPU_TARGETS=$(AMDGPU_TARGETS)
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DSD_VULKAN=ON -DGGML_VULKAN=ON
else ifeq ($(OS),Darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DSD_METAL=OFF -DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DSD_METAL=ON -DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
	endif
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DSD_SYCL=ON \
		-DGGML_SYCL_F16=ON
endif

ifeq ($(BUILD_TYPE),sycl_f32)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DSD_SYCL=ON
endif

sources/stablediffusion-ggml.cpp:
	git clone --recursive $(STABLEDIFFUSION_GGML_REPO) sources/stablediffusion-ggml.cpp && \
	cd sources/stablediffusion-ggml.cpp && \
	git checkout $(STABLEDIFFUSION_GGML_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)

# Only build CPU variants on Linux
ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = libgosd-avx.so libgosd-avx2.so libgosd-avx512.so libgosd-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant
	VARIANT_TARGETS = libgosd-fallback.so
endif

stablediffusion-ggml: main.go gosd.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o stablediffusion-ggml ./

package: stablediffusion-ggml
	bash package.sh

build: package

clean: purge
	rm -rf libgosd*.so stablediffusion-ggml package sources

purge:
	rm -rf build*

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
libgosd-avx.so: sources/stablediffusion-ggml.cpp
	$(MAKE) purge
	$(info ${GREEN}I stablediffusion-ggml build info:avx${RESET})
	SO_TARGET=libgosd-avx.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off" $(MAKE) libgosd-custom
	rm -rfv build*

libgosd-avx2.so: sources/stablediffusion-ggml.cpp
	$(MAKE) purge
	$(info ${GREEN}I stablediffusion-ggml build info:avx2${RESET})
	SO_TARGET=libgosd-avx2.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) libgosd-custom
	rm -rfv build*

libgosd-avx512.so: sources/stablediffusion-ggml.cpp
	$(MAKE) purge
	$(info ${GREEN}I stablediffusion-ggml build info:avx512${RESET})
	SO_TARGET=libgosd-avx512.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) libgosd-custom
	rm -rfv build*
endif

# Build fallback variant (all platforms)
libgosd-fallback.so: sources/stablediffusion-ggml.cpp
	$(MAKE) purge
	$(info ${GREEN}I stablediffusion-ggml build info:fallback${RESET})
	SO_TARGET=libgosd-fallback.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off" $(MAKE) libgosd-custom
	rm -rfv build*

libgosd-custom: CMakeLists.txt gosd.cpp gosd.h
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) && \
	cd .. && \
	mv build-$(SO_TARGET)/libgosd.so ./$(SO_TARGET)

all: stablediffusion-ggml package