CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# sam3.cpp
SAM3_REPO?=https://github.com/PABannier/sam3.cpp
SAM3_VERSION?=01832ef85fcc8eb6488f1d01cd247f07e96ff5a9

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# If build type is cublas, then we set -DGGML_CUDA=ON to CMAKE_ARGS automatically
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifeq ($(BUILD_TYPE),clblas)
	CMAKE_ARGS+=-DGGML_CLBLAST=ON
else ifeq ($(BUILD_TYPE),hipblas)
	ROCM_HOME ?= /opt/rocm
	ROCM_PATH ?= /opt/rocm
	export CXX=$(ROCM_HOME)/llvm/bin/clang++
	export CC=$(ROCM_HOME)/llvm/bin/clang
	AMDGPU_TARGETS?=gfx908,gfx90a,gfx942,gfx950,gfx1030,gfx1100,gfx1101,gfx1102,gfx1200,gfx1201
	CMAKE_ARGS+=-DGGML_HIPBLAS=ON -DAMDGPU_TARGETS=$(AMDGPU_TARGETS)
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=ON
else ifeq ($(OS),Darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
	endif
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DGGML_SYCL_F16=ON
endif

ifeq ($(BUILD_TYPE),sycl_f32)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx
endif

sources/sam3.cpp:
	git clone --recursive $(SAM3_REPO) sources/sam3.cpp && \
	cd sources/sam3.cpp && \
	git checkout $(SAM3_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)

# Only build CPU variants on Linux
ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = libgosam3-avx.so libgosam3-avx2.so libgosam3-avx512.so libgosam3-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant
	VARIANT_TARGETS = libgosam3-fallback.so
endif

sam3-cpp: main.go gosam3.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o sam3-cpp ./

package: sam3-cpp
	bash package.sh

build: package

clean: purge
	rm -rf libgosam3*.so sam3-cpp package sources

purge:
	rm -rf build*

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
libgosam3-avx.so: sources/sam3.cpp
	$(MAKE) purge
	$(info ${GREEN}I sam3-cpp build info:avx${RESET})
	SO_TARGET=libgosam3-avx.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgosam3-custom
	rm -rfv build*

libgosam3-avx2.so: sources/sam3.cpp
	$(MAKE) purge
	$(info ${GREEN}I sam3-cpp build info:avx2${RESET})
	SO_TARGET=libgosam3-avx2.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgosam3-custom
	rm -rfv build*

libgosam3-avx512.so: sources/sam3.cpp
	$(MAKE) purge
	$(info ${GREEN}I sam3-cpp build info:avx512${RESET})
	SO_TARGET=libgosam3-avx512.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgosam3-custom
	rm -rfv build*
endif

# Build fallback variant (all platforms)
libgosam3-fallback.so: sources/sam3.cpp
	$(MAKE) purge
	$(info ${GREEN}I sam3-cpp build info:fallback${RESET})
	SO_TARGET=libgosam3-fallback.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgosam3-custom
	rm -rfv build*

libgosam3-custom: CMakeLists.txt gosam3.cpp gosam3.h
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) && \
	cd .. && \
	mv build-$(SO_TARGET)/libgosam3.so ./$(SO_TARGET)

all: sam3-cpp package
