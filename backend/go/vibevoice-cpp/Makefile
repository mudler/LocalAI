CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# vibevoice.cpp version
VIBEVOICE_REPO?=https://github.com/mudler/vibevoice.cpp
VIBEVOICE_CPP_VERSION?=master
SO_TARGET?=libgovibevoicecpp.so

CMAKE_ARGS+=-DBUILD_SHARED_LIBS=OFF
CMAKE_ARGS+=-DVIBEVOICE_BUILD_TESTS=OFF
CMAKE_ARGS+=-DVIBEVOICE_BUILD_EXAMPLES=OFF

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON -DVIBEVOICE_GGML_CUDA=ON
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifeq ($(BUILD_TYPE),clblas)
	CMAKE_ARGS+=-DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DGGML_HIPBLAS=ON -DVIBEVOICE_GGML_HIPBLAS=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=ON -DVIBEVOICE_GGML_VULKAN=ON
else ifeq ($(OS),Darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DGGML_METAL=ON -DVIBEVOICE_GGML_METAL=ON
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

sources/vibevoice.cpp:
	mkdir -p sources/vibevoice.cpp
	cd sources/vibevoice.cpp && \
	git init && \
	git remote add origin $(VIBEVOICE_REPO) && \
	git fetch origin && \
	git checkout $(VIBEVOICE_CPP_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)

# Only build CPU variants on Linux
ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = libgovibevoicecpp-avx.so libgovibevoicecpp-avx2.so libgovibevoicecpp-avx512.so libgovibevoicecpp-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant
	VARIANT_TARGETS = libgovibevoicecpp-fallback.so
endif

vibevoice-cpp: main.go govibevoicecpp.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o vibevoice-cpp ./

package: vibevoice-cpp
	bash package.sh

build: package

clean: purge
	rm -rf libgovibevoicecpp*.so package sources/vibevoice.cpp vibevoice-cpp

purge:
	rm -rf build*

# Variants must build sequentially
.NOTPARALLEL:

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
libgovibevoicecpp-avx.so: sources/vibevoice.cpp
	$(info ${GREEN}I vibevoice-cpp build info:avx${RESET})
	SO_TARGET=libgovibevoicecpp-avx.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgovibevoicecpp-custom
	rm -rf build-libgovibevoicecpp-avx.so

libgovibevoicecpp-avx2.so: sources/vibevoice.cpp
	$(info ${GREEN}I vibevoice-cpp build info:avx2${RESET})
	SO_TARGET=libgovibevoicecpp-avx2.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgovibevoicecpp-custom
	rm -rf build-libgovibevoicecpp-avx2.so

libgovibevoicecpp-avx512.so: sources/vibevoice.cpp
	$(info ${GREEN}I vibevoice-cpp build info:avx512${RESET})
	SO_TARGET=libgovibevoicecpp-avx512.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgovibevoicecpp-custom
	rm -rf build-libgovibevoicecpp-avx512.so
endif

# Build fallback variant (all platforms)
libgovibevoicecpp-fallback.so: sources/vibevoice.cpp
	$(info ${GREEN}I vibevoice-cpp build info:fallback${RESET})
	SO_TARGET=libgovibevoicecpp-fallback.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgovibevoicecpp-custom
	rm -rf build-libgovibevoicecpp-fallback.so

libgovibevoicecpp-custom: CMakeLists.txt cpp/govibevoicecpp.cpp cpp/govibevoicecpp.h
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) --target govibevoicecpp && \
	cd .. && \
	mv build-$(SO_TARGET)/libgovibevoicecpp.so ./$(SO_TARGET)

test: vibevoice-cpp
	@echo "Running vibevoice-cpp tests..."
	bash test.sh
	@echo "vibevoice-cpp tests completed."

all: vibevoice-cpp package
