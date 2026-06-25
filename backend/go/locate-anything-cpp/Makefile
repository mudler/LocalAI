CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# locate-anything.cpp. Pin to a specific commit for a stable build; leaving
# this on `master` always picks up the latest C-API surface (incl. the
# per-detection accessor functions used by golocateanythingcpp.go).
LOCATEANYTHING_REPO?=https://github.com/mudler/locate-anything.cpp.git
LOCATEANYTHING_VERSION?=92c1682da792c1e8a5dec91acc2be4b02c742ded

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# Forward LocalAI's BUILD_TYPE to the matching ggml backend switch.
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON -DLA_GGML_CUDA=ON
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
	CMAKE_ARGS+=-DGGML_VULKAN=ON -DLA_GGML_VULKAN=ON
else ifeq ($(OS),Darwin)
	# macOS/Metal: built + published as an OCI image by CI (includeDarwin in
	# .github/backend-matrix.yml) so Apple Silicon users can install this backend.
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
		CMAKE_ARGS+=-DLA_GGML_METAL=ON
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

sources/locate-anything.cpp:
	mkdir -p sources && \
	git clone --recursive $(LOCATEANYTHING_REPO) sources/locate-anything.cpp && \
	cd sources/locate-anything.cpp && \
	git checkout $(LOCATEANYTHING_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)

# Only build CPU variants on Linux
ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = liblocateanythingcpp-avx.so liblocateanythingcpp-avx2.so liblocateanythingcpp-avx512.so liblocateanythingcpp-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant
	VARIANT_TARGETS = liblocateanythingcpp-fallback.dylib
endif

locate-anything-cpp: main.go golocateanythingcpp.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o locate-anything-cpp ./

package: locate-anything-cpp
	bash package.sh

build: package

clean: purge
	rm -rf liblocateanythingcpp*.so liblocateanythingcpp*.dylib locate-anything-cpp package sources

purge:
	rm -rf build*

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
liblocateanythingcpp-avx.so: sources/locate-anything.cpp
	rm -rfv build-$@
	$(info ${GREEN}I locate-anything-cpp build info:avx${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) liblocateanythingcpp-custom
	rm -rfv build-$@

liblocateanythingcpp-avx2.so: sources/locate-anything.cpp
	rm -rfv build-$@
	$(info ${GREEN}I locate-anything-cpp build info:avx2${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) liblocateanythingcpp-custom
	rm -rfv build-$@

liblocateanythingcpp-avx512.so: sources/locate-anything.cpp
	rm -rfv build-$@
	$(info ${GREEN}I locate-anything-cpp build info:avx512${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) liblocateanythingcpp-custom
	rm -rfv build-$@
endif

# Build fallback variant (all platforms)
ifeq ($(UNAME_S),Darwin)
liblocateanythingcpp-fallback.dylib: sources/locate-anything.cpp
	rm -rfv build-$@
	$(info ${GREEN}I locate-anything-cpp build info:fallback${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) liblocateanythingcpp-custom
	rm -rfv build-$@
else
liblocateanythingcpp-fallback.so: sources/locate-anything.cpp
	rm -rfv build-$@
	$(info ${GREEN}I locate-anything-cpp build info:fallback${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) liblocateanythingcpp-custom
	rm -rfv build-$@
endif

liblocateanythingcpp-custom: CMakeLists.txt
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) && \
	cd .. && \
	(mv build-$(SO_TARGET)/liblocateanythingcpp.so ./$(SO_TARGET) 2>/dev/null || \
	 mv build-$(SO_TARGET)/liblocateanythingcpp.dylib ./$(SO_TARGET) 2>/dev/null)

all: locate-anything-cpp package

# `test` is invoked by the top-level Makefile's `test-extra` target. It builds
# the backend binary + the fallback shared library (needed for dlopen at
# runtime), then runs test.sh which downloads the q8_0 GGUF + COCO image and
# exercises the gRPC Load/Detect wire path via the Go smoke test in
# main_test.go.
test: locate-anything-cpp liblocateanythingcpp-fallback.so
	bash test.sh
