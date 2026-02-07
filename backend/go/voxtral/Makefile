.NOTPARALLEL:

CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=true

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

# voxtral.c version
VOXTRAL_REPO?=https://github.com/antirez/voxtral.c
VOXTRAL_VERSION?=8f810dd23c44be5453cb46c92216a3eaab46e85f

# Detect OS
UNAME_S := $(shell uname -s)

# Shared library extension
ifeq ($(UNAME_S),Darwin)
	SO_EXT=dylib
else
	SO_EXT=so
endif

SO_TARGET?=libgovoxtral.$(SO_EXT)

CMAKE_ARGS+=-DBUILD_SHARED_LIBS=OFF

ifeq ($(NATIVE),false)
ifneq ($(UNAME_S),Darwin)
	CMAKE_ARGS+=-DCMAKE_C_FLAGS="-march=x86-64"
endif
endif

ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DUSE_OPENBLAS=OFF
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DUSE_OPENBLAS=OFF
else ifeq ($(BUILD_TYPE),metal)
	CMAKE_ARGS+=-DUSE_OPENBLAS=OFF -DUSE_METAL=ON
else ifeq ($(UNAME_S),Darwin)
	# Default on macOS: use Accelerate (no OpenBLAS needed)
	CMAKE_ARGS+=-DUSE_OPENBLAS=OFF
else
	CMAKE_ARGS+=-DUSE_OPENBLAS=ON
endif

# Only build CPU variants on Linux; on Darwin build a single dylib
ifeq ($(UNAME_S),Linux)
VARIANT_TARGETS = libgovoxtral-avx.so libgovoxtral-avx2.so libgovoxtral-fallback.so
else ifeq ($(UNAME_S),Darwin)
VARIANT_TARGETS = libgovoxtral-fallback.dylib
else
VARIANT_TARGETS = libgovoxtral-fallback.so
endif

sources/voxtral.c:
	mkdir -p sources/voxtral.c
	cd sources/voxtral.c && \
	git init && \
	git remote add origin $(VOXTRAL_REPO) && \
	git fetch origin && \
	git checkout $(VOXTRAL_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

voxtral: main.go govoxtral.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o voxtral ./

package: voxtral
	bash package.sh

build: package

clean: purge
	rm -rf libgovoxtral*.so libgovoxtral*.dylib package sources/voxtral.c voxtral

purge:
	rm -rf build*

# Build all variants (Linux only)
ifeq ($(UNAME_S),Linux)
libgovoxtral-avx.so: sources/voxtral.c
	$(MAKE) purge
	$(info Building voxtral: avx)
	SO_TARGET=libgovoxtral-avx.so CMAKE_ARGS="$(CMAKE_ARGS) -DCMAKE_C_FLAGS='-mavx -mno-avx2 -mno-avx512f -mno-fma -mno-f16c'" $(MAKE) libgovoxtral-custom
	rm -rfv build*

libgovoxtral-avx2.so: sources/voxtral.c
	$(MAKE) purge
	$(info Building voxtral: avx2)
	SO_TARGET=libgovoxtral-avx2.so CMAKE_ARGS="$(CMAKE_ARGS) -DCMAKE_C_FLAGS='-mavx -mavx2 -mfma -mf16c'" $(MAKE) libgovoxtral-custom
	rm -rfv build*
endif

# Build fallback variant
ifeq ($(UNAME_S),Darwin)
libgovoxtral-fallback.dylib: sources/voxtral.c
	$(MAKE) purge
	$(info Building voxtral: darwin fallback)
	SO_TARGET=libgovoxtral-fallback.dylib NATIVE=true $(MAKE) libgovoxtral-custom
	rm -rfv build*
else
libgovoxtral-fallback.so: sources/voxtral.c
	$(MAKE) purge
	$(info Building voxtral: fallback)
	SO_TARGET=libgovoxtral-fallback.so CMAKE_ARGS="$(CMAKE_ARGS) -DCMAKE_C_FLAGS='-mno-avx -mno-avx2 -mno-avx512f -mno-fma -mno-f16c'" $(MAKE) libgovoxtral-custom
	rm -rfv build*
endif

libgovoxtral-custom: CMakeLists.txt csrc/govoxtral.c csrc/govoxtral.h
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) && \
	cd .. && \
	(mv build-$(SO_TARGET)/libgovoxtral.so ./$(SO_TARGET) 2>/dev/null || \
	 mv build-$(SO_TARGET)/libgovoxtral.dylib ./$(SO_TARGET) 2>/dev/null)

test: voxtral
	@echo "Running voxtral tests..."
	bash test.sh
	@echo "voxtral tests completed."

all: voxtral package
