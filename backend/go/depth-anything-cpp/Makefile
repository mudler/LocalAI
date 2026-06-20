CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# depth-anything.cpp. Pin to a specific commit for a stable build; a squash
# merge upstream can orphan a branch, so the native version is pinned by SHA.
# This SHA adds the Depth Anything V2 engine + C-API routing (depth-only,
# relative + metric) on top of the nested two-file metric C-API (abi_version 4,
# da_capi_load_nested) required by the depth-anything-3-nested gallery model.
# It is kept alive by the upstream tag da2-support (survives a squash-merge);
# repoint to the master merge commit once mudler/depth-anything.cpp PR #1 lands.
DEPTHANYTHING_REPO?=https://github.com/mudler/depth-anything.cpp.git
DEPTHANYTHING_VERSION?=e3dec57f13a52366bbc4f279ef44804915960a6b

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# Forward LocalAI's BUILD_TYPE to the matching ggml backend switch. depth-anything.cpp
# force-sets GGML_CUDA/GGML_VULKAN/GGML_METAL from its own DA_GGML_* options, so
# those must be toggled via the DA_GGML_* names (a bare -DGGML_CUDA=ON would be
# overridden); the remaining ggml switches pass straight through.
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON -DDA_GGML_CUDA=ON
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
	CMAKE_ARGS+=-DGGML_VULKAN=ON -DDA_GGML_VULKAN=ON
else ifeq ($(OS),Darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
		CMAKE_ARGS+=-DDA_GGML_METAL=ON
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

sources/depth-anything.cpp:
	mkdir -p sources && \
	git clone --recursive $(DEPTHANYTHING_REPO) sources/depth-anything.cpp && \
	cd sources/depth-anything.cpp && \
	git checkout $(DEPTHANYTHING_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)

# Only build CPU variants on Linux
ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = libdepthanythingcpp-avx.so libdepthanythingcpp-avx2.so libdepthanythingcpp-avx512.so libdepthanythingcpp-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant
	VARIANT_TARGETS = libdepthanythingcpp-fallback.so
endif

depth-anything-cpp: main.go godepthanythingcpp.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o depth-anything-cpp ./

package: depth-anything-cpp
	bash package.sh

build: package

clean: purge
	rm -rf libdepthanythingcpp*.so depth-anything-cpp package sources

purge:
	rm -rf build*

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
libdepthanythingcpp-avx.so: sources/depth-anything.cpp
	rm -rfv build-$@
	$(info ${GREEN}I depth-anything-cpp build info:avx${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libdepthanythingcpp-custom
	rm -rfv build-$@

libdepthanythingcpp-avx2.so: sources/depth-anything.cpp
	rm -rfv build-$@
	$(info ${GREEN}I depth-anything-cpp build info:avx2${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libdepthanythingcpp-custom
	rm -rfv build-$@

libdepthanythingcpp-avx512.so: sources/depth-anything.cpp
	rm -rfv build-$@
	$(info ${GREEN}I depth-anything-cpp build info:avx512${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libdepthanythingcpp-custom
	rm -rfv build-$@
endif

# Build fallback variant (all platforms)
libdepthanythingcpp-fallback.so: sources/depth-anything.cpp
	rm -rfv build-$@
	$(info ${GREEN}I depth-anything-cpp build info:fallback${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libdepthanythingcpp-custom
	rm -rfv build-$@

libdepthanythingcpp-custom: CMakeLists.txt
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) && \
	cd .. && \
	mv build-$(SO_TARGET)/libdepthanything.so ./$(SO_TARGET)

all: depth-anything-cpp package

# `test` is invoked by the top-level Makefile's `test-extra` target. It builds
# the backend binary + the fallback shared library (needed for dlopen at
# runtime), then runs test.sh which downloads a small GGUF + a test image and
# exercises the gRPC Load/Predict wire path via the Go smoke test in
# main_test.go.
test: depth-anything-cpp libdepthanythingcpp-fallback.so
	bash test.sh
