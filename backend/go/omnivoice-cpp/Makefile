CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# omnivoice.cpp version
OMNIVOICE_REPO?=https://github.com/ServeurpersoCom/omnivoice.cpp
OMNIVOICE_VERSION?=f39cc4a3af988091f662313b336dddf8c83a3fb5
SO_TARGET?=libgomnivoicecpp.so

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

sources/omnivoice.cpp:
	mkdir -p sources/omnivoice.cpp
	cd sources/omnivoice.cpp && \
	git init && \
	git remote add origin $(OMNIVOICE_REPO) && \
	git fetch origin && \
	git checkout $(OMNIVOICE_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)

# Only build CPU variants on Linux
ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = libgomnivoicecpp-avx.so libgomnivoicecpp-avx2.so libgomnivoicecpp-avx512.so libgomnivoicecpp-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant (as a dylib)
	VARIANT_TARGETS = libgomnivoicecpp-fallback.dylib
endif

omnivoice-cpp: main.go gomnivoicecpp.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o omnivoice-cpp ./

package: omnivoice-cpp
	bash package.sh

build: package

clean: purge
	rm -rf libgomnivoicecpp*.so libgomnivoicecpp*.dylib package sources/omnivoice.cpp omnivoice-cpp

purge:
	rm -rf build*

.NOTPARALLEL:

ifeq ($(UNAME_S),Linux)
libgomnivoicecpp-avx.so: sources/omnivoice.cpp
	$(info ${GREEN}I omnivoice-cpp build info:avx${RESET})
	SO_TARGET=libgomnivoicecpp-avx.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgomnivoicecpp-custom
	rm -rf build-libgomnivoicecpp-avx.so

libgomnivoicecpp-avx2.so: sources/omnivoice.cpp
	$(info ${GREEN}I omnivoice-cpp build info:avx2${RESET})
	SO_TARGET=libgomnivoicecpp-avx2.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgomnivoicecpp-custom
	rm -rf build-libgomnivoicecpp-avx2.so

libgomnivoicecpp-avx512.so: sources/omnivoice.cpp
	$(info ${GREEN}I omnivoice-cpp build info:avx512${RESET})
	SO_TARGET=libgomnivoicecpp-avx512.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgomnivoicecpp-custom
	rm -rf build-libgomnivoicecpp-avx512.so
endif

libgomnivoicecpp-fallback.so: sources/omnivoice.cpp
	$(info ${GREEN}I omnivoice-cpp build info:fallback${RESET})
	SO_TARGET=libgomnivoicecpp-fallback.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgomnivoicecpp-custom
	rm -rf build-libgomnivoicecpp-fallback.so

# Build fallback variant as a dylib (Darwin)
libgomnivoicecpp-fallback.dylib: sources/omnivoice.cpp
	$(info ${GREEN}I omnivoice-cpp build info:fallback (dylib)${RESET})
	SO_TARGET=libgomnivoicecpp-fallback.dylib CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgomnivoicecpp-custom
	rm -rf build-libgomnivoicecpp-fallback.dylib

libgomnivoicecpp-custom: CMakeLists.txt cpp/gomnivoicecpp.cpp cpp/gomnivoicecpp.h
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) --target gomnivoicecpp && \
	cd .. && \
	(mv build-$(SO_TARGET)/libgomnivoicecpp.so ./$(SO_TARGET) 2>/dev/null || \
	 mv build-$(SO_TARGET)/libgomnivoicecpp.dylib ./$(SO_TARGET) 2>/dev/null)

test: omnivoice-cpp
	@echo "Running omnivoice-cpp tests..."
	bash test.sh
	@echo "omnivoice-cpp tests completed."

all: omnivoice-cpp package
