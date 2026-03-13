CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# acestep.cpp version
ACESTEP_REPO?=https://github.com/ace-step/acestep.cpp
ACESTEP_CPP_VERSION?=5aa065445541094cba934299cd498bbb9fa5c434
SO_TARGET?=libgoacestepcpp.so

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

sources/acestep.cpp:
	mkdir -p sources/acestep.cpp
	cd sources/acestep.cpp && \
	git init && \
	git remote add origin $(ACESTEP_REPO) && \
	git fetch origin && \
	git checkout $(ACESTEP_CPP_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)

# Only build CPU variants on Linux
ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = libgoacestepcpp-avx.so libgoacestepcpp-avx2.so libgoacestepcpp-avx512.so libgoacestepcpp-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant
	VARIANT_TARGETS = libgoacestepcpp-fallback.so
endif

acestep-cpp: main.go goacestepcpp.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o acestep-cpp ./

package: acestep-cpp
	bash package.sh

build: package

clean: purge
	rm -rf libgoacestepcpp*.so package sources/acestep.cpp acestep-cpp

purge:
	rm -rf build*

# Variants must build sequentially: each uses its own build-<name> directory,
# but parallel builds can still race on shared resources (jobserver, disk I/O).
.NOTPARALLEL:

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
libgoacestepcpp-avx.so: sources/acestep.cpp
	$(info ${GREEN}I acestep-cpp build info:avx${RESET})
	SO_TARGET=libgoacestepcpp-avx.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgoacestepcpp-custom
	rm -rf build-libgoacestepcpp-avx.so

libgoacestepcpp-avx2.so: sources/acestep.cpp
	$(info ${GREEN}I acestep-cpp build info:avx2${RESET})
	SO_TARGET=libgoacestepcpp-avx2.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgoacestepcpp-custom
	rm -rf build-libgoacestepcpp-avx2.so

libgoacestepcpp-avx512.so: sources/acestep.cpp
	$(info ${GREEN}I acestep-cpp build info:avx512${RESET})
	SO_TARGET=libgoacestepcpp-avx512.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgoacestepcpp-custom
	rm -rf build-libgoacestepcpp-avx512.so
endif

# Build fallback variant (all platforms)
libgoacestepcpp-fallback.so: sources/acestep.cpp
	$(info ${GREEN}I acestep-cpp build info:fallback${RESET})
	SO_TARGET=libgoacestepcpp-fallback.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgoacestepcpp-custom
	rm -rf build-libgoacestepcpp-fallback.so

libgoacestepcpp-custom: CMakeLists.txt cpp/goacestepcpp.cpp cpp/goacestepcpp.h
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) --target goacestepcpp && \
	cd .. && \
	mv build-$(SO_TARGET)/libgoacestepcpp.so ./$(SO_TARGET)

test: acestep-cpp
	@echo "Running acestep-cpp tests..."
	bash test.sh
	@echo "acestep-cpp tests completed."

all: acestep-cpp package
