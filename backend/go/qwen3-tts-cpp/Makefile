CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# qwen3-tts.cpp version
QWEN3TTS_REPO?=https://github.com/predict-woo/qwen3-tts.cpp
QWEN3TTS_CPP_VERSION?=7a762e2ad4bacc6fdda81d81bf10a09ffb546f29
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
	CMAKE_ARGS+=-DGGML_HIPBLAS=ON
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

sources/qwen3-tts.cpp:
	mkdir -p sources/qwen3-tts.cpp
	cd sources/qwen3-tts.cpp && \
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
	# On non-Linux (e.g., Darwin), build only fallback variant
	VARIANT_TARGETS = libgoqwen3ttscpp-fallback.so
endif

qwen3-tts-cpp: main.go goqwen3ttscpp.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o qwen3-tts-cpp ./

package: qwen3-tts-cpp
	bash package.sh

build: package

clean: purge
	rm -rf libgoqwen3ttscpp*.so package sources/qwen3-tts.cpp qwen3-tts-cpp

purge:
	rm -rf build*

# Variants must build sequentially
.NOTPARALLEL:

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
libgoqwen3ttscpp-avx.so: sources/qwen3-tts.cpp
	$(info ${GREEN}I qwen3-tts-cpp build info:avx${RESET})
	SO_TARGET=libgoqwen3ttscpp-avx.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgoqwen3ttscpp-custom
	rm -rf build-libgoqwen3ttscpp-avx.so

libgoqwen3ttscpp-avx2.so: sources/qwen3-tts.cpp
	$(info ${GREEN}I qwen3-tts-cpp build info:avx2${RESET})
	SO_TARGET=libgoqwen3ttscpp-avx2.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgoqwen3ttscpp-custom
	rm -rf build-libgoqwen3ttscpp-avx2.so

libgoqwen3ttscpp-avx512.so: sources/qwen3-tts.cpp
	$(info ${GREEN}I qwen3-tts-cpp build info:avx512${RESET})
	SO_TARGET=libgoqwen3ttscpp-avx512.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgoqwen3ttscpp-custom
	rm -rf build-libgoqwen3ttscpp-avx512.so
endif

# Build fallback variant (all platforms)
libgoqwen3ttscpp-fallback.so: sources/qwen3-tts.cpp
	$(info ${GREEN}I qwen3-tts-cpp build info:fallback${RESET})
	SO_TARGET=libgoqwen3ttscpp-fallback.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgoqwen3ttscpp-custom
	rm -rf build-libgoqwen3ttscpp-fallback.so

libgoqwen3ttscpp-custom: CMakeLists.txt cpp/goqwen3ttscpp.cpp cpp/goqwen3ttscpp.h
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) --target goqwen3ttscpp && \
	cd .. && \
	mv build-$(SO_TARGET)/libgoqwen3ttscpp.so ./$(SO_TARGET)

test: qwen3-tts-cpp
	@echo "Running qwen3-tts-cpp tests..."
	bash test.sh
	@echo "qwen3-tts-cpp tests completed."

all: qwen3-tts-cpp package
