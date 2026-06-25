CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# rt-detr.cpp (GitHub redirects the historical mudler/rt-detr.cpp to the new
# mudler/rf-detr.cpp slug). Pin to a specific commit if you need a stable
# build; leaving this on `master` always picks up the latest C-API surface
# (incl. the per-detection accessor functions used by gorfdetrcpp.go).
RFDETR_REPO?=https://github.com/mudler/rf-detr.cpp.git
RFDETR_VERSION?=65c0ffcc9a9bc9dae38252f63d0417c9845a6cf7

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# Forward LocalAI's BUILD_TYPE to the matching ggml backend switch.
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON -DRFDETR_GGML_CUDA=ON
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
	CMAKE_ARGS+=-DGGML_HIPBLAS=ON -DRFDETR_GGML_HIPBLAS=ON -DAMDGPU_TARGETS=$(AMDGPU_TARGETS)
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=ON -DRFDETR_GGML_VULKAN=ON
else ifeq ($(OS),Darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
		CMAKE_ARGS+=-DRFDETR_GGML_METAL=ON
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

sources/rt-detr.cpp:
	mkdir -p sources && \
	git clone --recursive $(RFDETR_REPO) sources/rt-detr.cpp && \
	cd sources/rt-detr.cpp && \
	git checkout $(RFDETR_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)

# Only build CPU variants on Linux
ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = librfdetrcpp-avx.so librfdetrcpp-avx2.so librfdetrcpp-avx512.so librfdetrcpp-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant
	VARIANT_TARGETS = librfdetrcpp-fallback.dylib
endif

rfdetr-cpp: main.go gorfdetrcpp.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o rfdetr-cpp ./

package: rfdetr-cpp
	bash package.sh

build: package

clean: purge
	rm -rf librfdetrcpp*.so librfdetrcpp*.dylib rfdetr-cpp package sources

purge:
	rm -rf build*

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
librfdetrcpp-avx.so: sources/rt-detr.cpp
	rm -rfv build-$@
	$(info ${GREEN}I rfdetr-cpp build info:avx${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) librfdetrcpp-custom
	rm -rfv build-$@

librfdetrcpp-avx2.so: sources/rt-detr.cpp
	rm -rfv build-$@
	$(info ${GREEN}I rfdetr-cpp build info:avx2${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) librfdetrcpp-custom
	rm -rfv build-$@

librfdetrcpp-avx512.so: sources/rt-detr.cpp
	rm -rfv build-$@
	$(info ${GREEN}I rfdetr-cpp build info:avx512${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) librfdetrcpp-custom
	rm -rfv build-$@
endif

# Build fallback variant (all platforms)
ifeq ($(UNAME_S),Darwin)
librfdetrcpp-fallback.dylib: sources/rt-detr.cpp
	rm -rfv build-$@
	$(info ${GREEN}I rfdetr-cpp build info:fallback${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) librfdetrcpp-custom
	rm -rfv build-$@
else
librfdetrcpp-fallback.so: sources/rt-detr.cpp
	rm -rfv build-$@
	$(info ${GREEN}I rfdetr-cpp build info:fallback${RESET})
	SO_TARGET=$@ CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) librfdetrcpp-custom
	rm -rfv build-$@
endif

librfdetrcpp-custom: CMakeLists.txt
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) && \
	cd .. && \
	(mv build-$(SO_TARGET)/librfdetrcpp.so ./$(SO_TARGET) 2>/dev/null || \
	 mv build-$(SO_TARGET)/librfdetrcpp.dylib ./$(SO_TARGET) 2>/dev/null)

all: rfdetr-cpp package

# `test` is invoked by the top-level Makefile's `test-extra` target. It builds
# the backend binary + the fallback shared library (needed for dlopen at
# runtime), then runs test.sh which downloads the test models + COCO image
# and exercises the gRPC Load/Detect wire path via the Go smoke test in
# main_test.go for both the detection and segmentation models.
test: rfdetr-cpp librfdetrcpp-fallback.so
	bash test.sh
