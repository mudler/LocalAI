CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# moss-tts.cpp version
MOSSTTS_REPO?=https://github.com/mudler/moss-tts.cpp
MOSSTTS_CPP_VERSION?=b90b28fb22cf593b83fa51586b0e0f29211f5594
SO_TARGET?=libgomosstts-cpp.so

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

sources/moss-tts.cpp:
	mkdir -p sources/moss-tts.cpp
	cd sources/moss-tts.cpp && \
	git init && \
	git remote add origin $(MOSSTTS_REPO) && \
	git fetch origin && \
	git checkout $(MOSSTTS_CPP_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)

# Only build CPU variants on Linux
ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = libgomosstts-cpp-avx.so libgomosstts-cpp-avx2.so libgomosstts-cpp-avx512.so libgomosstts-cpp-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant (as a dylib)
	VARIANT_TARGETS = libgomosstts-cpp-fallback.dylib
endif

moss-tts-cpp: main.go gomossttscpp.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o moss-tts-cpp ./

package: moss-tts-cpp
	bash package.sh

build: package

clean: purge
	rm -rf libgomosstts-cpp*.so libgomosstts-cpp*.dylib package sources/moss-tts.cpp moss-tts-cpp

purge:
	rm -rf build*

# Variants must build sequentially
.NOTPARALLEL:

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
libgomosstts-cpp-avx.so: sources/moss-tts.cpp
	$(info ${GREEN}I moss-tts-cpp build info:avx${RESET})
	SO_TARGET=libgomosstts-cpp-avx.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgomosstts-cpp-custom
	rm -rf build-libgomosstts-cpp-avx.so

libgomosstts-cpp-avx2.so: sources/moss-tts.cpp
	$(info ${GREEN}I moss-tts-cpp build info:avx2${RESET})
	SO_TARGET=libgomosstts-cpp-avx2.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgomosstts-cpp-custom
	rm -rf build-libgomosstts-cpp-avx2.so

libgomosstts-cpp-avx512.so: sources/moss-tts.cpp
	$(info ${GREEN}I moss-tts-cpp build info:avx512${RESET})
	SO_TARGET=libgomosstts-cpp-avx512.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgomosstts-cpp-custom
	rm -rf build-libgomosstts-cpp-avx512.so
endif

# Build fallback variant (all platforms)
libgomosstts-cpp-fallback.so: sources/moss-tts.cpp
	$(info ${GREEN}I moss-tts-cpp build info:fallback${RESET})
	SO_TARGET=libgomosstts-cpp-fallback.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgomosstts-cpp-custom
	rm -rf build-libgomosstts-cpp-fallback.so

# Build fallback variant as a dylib (Darwin)
libgomosstts-cpp-fallback.dylib: sources/moss-tts.cpp
	$(info ${GREEN}I moss-tts-cpp build info:fallback (dylib)${RESET})
	SO_TARGET=libgomosstts-cpp-fallback.dylib CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgomosstts-cpp-custom
	rm -rf build-libgomosstts-cpp-fallback.dylib

libgomosstts-cpp-custom: CMakeLists.txt cpp/mossttscpp.cpp cpp/mossttscpp.h
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) --target gomosstts-cpp && \
	cd .. && \
	(mv build-$(SO_TARGET)/libgomosstts-cpp.so ./$(SO_TARGET) 2>/dev/null || \
	 mv build-$(SO_TARGET)/libgomosstts-cpp.dylib ./$(SO_TARGET) 2>/dev/null)

test: moss-tts-cpp
	@echo "Running moss-tts-cpp tests..."
	bash test.sh
	@echo "moss-tts-cpp tests completed."

all: moss-tts-cpp package
