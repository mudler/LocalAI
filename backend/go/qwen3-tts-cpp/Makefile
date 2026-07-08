CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# qwentts.cpp version
QWEN3TTS_REPO?=https://github.com/ServeurpersoCom/qwentts.cpp
QWEN3TTS_CPP_VERSION?=e93b3fedd53504c29c1c1a9bed4fbe722bbb1df5
SO_TARGET?=libgoqwen3ttscpp.so

CMAKE_ARGS+=-DBUILD_SHARED_LIBS=OFF

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifeq ($(BUILD_TYPE),clblas)
	CMAKE_ARGS+=-DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
else ifeq ($(BUILD_TYPE),hipblas)
	# This ggml only understands GGML_HIP (GGML_HIPBLAS was removed upstream),
	# so passing GGML_HIPBLAS silently produced a CPU-only build (see #10666).
	ROCM_HOME ?= /opt/rocm
	ROCM_PATH ?= /opt/rocm
	export CXX=$(ROCM_HOME)/llvm/bin/clang++
	export CC=$(ROCM_HOME)/llvm/bin/clang
	AMDGPU_TARGETS ?= gfx908,gfx90a,gfx942,gfx950,gfx1030,gfx1100,gfx1101,gfx1102,gfx1151,gfx1200,gfx1201
	CMAKE_ARGS+=-DGGML_HIP=ON -DAMDGPU_TARGETS=$(AMDGPU_TARGETS)
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

sources/qwentts.cpp:
	mkdir -p sources/qwentts.cpp
	cd sources/qwentts.cpp && \
	git init && \
	git remote add origin $(QWEN3TTS_REPO) && \
	git fetch origin && \
	git checkout $(QWEN3TTS_CPP_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)

# Only build CPU variants on Linux
ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = libgoqwen3ttscpp-avx.so libgoqwen3ttscpp-avx2.so libgoqwen3ttscpp-avx512.so libgoqwen3ttscpp-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant (as a dylib)
	VARIANT_TARGETS = libgoqwen3ttscpp-fallback.dylib
endif

qwen3-tts-cpp: main.go goqwen3ttscpp.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o qwen3-tts-cpp ./

package: qwen3-tts-cpp
	bash package.sh

build: package

clean: purge
	rm -rf libgoqwen3ttscpp*.so libgoqwen3ttscpp*.dylib package sources/qwentts.cpp qwen3-tts-cpp

purge:
	rm -rf build*

# Variants must build sequentially
.NOTPARALLEL:

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
libgoqwen3ttscpp-avx.so: sources/qwentts.cpp
	$(info ${GREEN}I qwen3-tts-cpp build info:avx${RESET})
	SO_TARGET=libgoqwen3ttscpp-avx.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgoqwen3ttscpp-custom
	rm -rf build-libgoqwen3ttscpp-avx.so

libgoqwen3ttscpp-avx2.so: sources/qwentts.cpp
	$(info ${GREEN}I qwen3-tts-cpp build info:avx2${RESET})
	SO_TARGET=libgoqwen3ttscpp-avx2.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgoqwen3ttscpp-custom
	rm -rf build-libgoqwen3ttscpp-avx2.so

libgoqwen3ttscpp-avx512.so: sources/qwentts.cpp
	$(info ${GREEN}I qwen3-tts-cpp build info:avx512${RESET})
	SO_TARGET=libgoqwen3ttscpp-avx512.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgoqwen3ttscpp-custom
	rm -rf build-libgoqwen3ttscpp-avx512.so
endif

# Build fallback variant (all platforms)
libgoqwen3ttscpp-fallback.so: sources/qwentts.cpp
	$(info ${GREEN}I qwen3-tts-cpp build info:fallback${RESET})
	SO_TARGET=libgoqwen3ttscpp-fallback.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgoqwen3ttscpp-custom
	rm -rf build-libgoqwen3ttscpp-fallback.so

# Build fallback variant as a dylib (Darwin)
libgoqwen3ttscpp-fallback.dylib: sources/qwentts.cpp
	$(info ${GREEN}I qwen3-tts-cpp build info:fallback (dylib)${RESET})
	SO_TARGET=libgoqwen3ttscpp-fallback.dylib CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgoqwen3ttscpp-custom
	rm -rf build-libgoqwen3ttscpp-fallback.dylib

libgoqwen3ttscpp-custom: CMakeLists.txt cpp/goqwen3ttscpp.cpp cpp/goqwen3ttscpp.h
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) --target goqwen3ttscpp && \
	cd .. && \
	(mv build-$(SO_TARGET)/libgoqwen3ttscpp.so ./$(SO_TARGET) 2>/dev/null || \
	 mv build-$(SO_TARGET)/libgoqwen3ttscpp.dylib ./$(SO_TARGET) 2>/dev/null)

test: qwen3-tts-cpp
	@echo "Running qwen3-tts-cpp tests..."
	bash test.sh
	@echo "qwen3-tts-cpp tests completed."

all: qwen3-tts-cpp package
