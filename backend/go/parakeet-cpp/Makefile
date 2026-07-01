# parakeet-cpp backend Makefile.
#
# Upstream pin lives below as PARAKEET_VERSION?=e8acc6172a94e20a952cf1843decace5d771a94b
# (.github/bump_deps.sh) can find and update it - matches the
# whisper.cpp / ds4 / vibevoice-cpp convention.
#
# Local dev shortcut: if you already have an out-of-tree parakeet.cpp
# build, you can symlink the .so + header into this directory and skip
# the clone/cmake steps entirely, e.g.:
#
#   ln -sf /path/to/parakeet.cpp/build-shared/libparakeet.so .
#   ln -sf /path/to/parakeet.cpp/include/parakeet_capi.h .
#   go build -o parakeet-cpp-grpc .
#
# That's what the L0 smoke test uses. The default target below does the
# proper clone-at-pin + cmake build so CI doesn't need a side-checkout.

PARAKEET_VERSION?=e8acc6172a94e20a952cf1843decace5d771a94b
PARAKEET_REPO?=https://github.com/mudler/parakeet.cpp

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

BUILD_TYPE?=
NATIVE?=false

# Build ggml statically into libparakeet.so (PIC) so the shared lib is
# self-contained: dlopen needs no libggml*.so alongside it, only system libs
# (libstdc++/libgomp/libc) that the runtime image already provides.
CMAKE_ARGS?=-DCMAKE_BUILD_TYPE=Release -DPARAKEET_SHARED=ON -DPARAKEET_BUILD_CLI=OFF -DBUILD_SHARED_LIBS=OFF -DCMAKE_POSITION_INDEPENDENT_CODE=ON

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# parakeet.cpp gates its GGML backends behind PARAKEET_GGML_* options and does
# set(GGML_CUDA ${PARAKEET_GGML_CUDA} CACHE BOOL "" FORCE), so a bare -DGGML_CUDA=ON
# is overwritten back to OFF and the build silently falls back to CPU. Forward the
# PARAKEET_GGML_* options instead. (openblas is not gated, so -DGGML_BLAS passes through.)
ifeq ($(BUILD_TYPE),cublas)
	# GGML_CUDA_GRAPHS is OFF by ggml default; enabling it gives a small free
	# speedup (~1% measured on GB10, never negative) by capturing/replaying the
	# CUDA graph. Not gated by parakeet.cpp, so it passes straight through to ggml.
	CMAKE_ARGS+=-DPARAKEET_GGML_CUDA=ON -DGGML_CUDA_GRAPHS=ON
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DPARAKEET_GGML_HIP=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DPARAKEET_GGML_VULKAN=ON
endif

.PHONY: parakeet-cpp-grpc package build clean purge test all

all: parakeet-cpp-grpc

# Clone the upstream parakeet.cpp source at the pinned commit. Directory
# acts as the target so make only re-clones when missing. After a
# PARAKEET_VERSION bump, run 'make purge && make' to refetch.
sources/parakeet.cpp:
	mkdir -p sources/parakeet.cpp
	cd sources/parakeet.cpp && \
	git init -q && \
	git remote add origin $(PARAKEET_REPO) && \
	git fetch --depth 1 origin $(PARAKEET_VERSION) && \
	git checkout FETCH_HEAD && \
	git submodule update --init --recursive --depth 1 --single-branch

# Build the shared lib + header out-of-tree, then stage them next to the
# Go sources so purego.Dlopen("libparakeet.so") and the cgo-less build
# both pick them up.
libparakeet.so: sources/parakeet.cpp
	cmake -B sources/parakeet.cpp/build-shared -S sources/parakeet.cpp $(CMAKE_ARGS)
	cmake --build sources/parakeet.cpp/build-shared --config Release -j$(JOBS)
	cp -fv sources/parakeet.cpp/build-shared/libparakeet.so* ./ 2>/dev/null || true
	cp -fv sources/parakeet.cpp/build-shared/libparakeet.dylib ./ 2>/dev/null || true
	cp -fv sources/parakeet.cpp/include/parakeet_capi.h ./

parakeet-cpp-grpc: libparakeet.so main.go goparakeetcpp.go
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o parakeet-cpp-grpc .

package: parakeet-cpp-grpc
	bash package.sh

build: package

# Test target. Smoke test is gated on PARAKEET_BACKEND_TEST_MODEL +
# PARAKEET_BACKEND_TEST_WAV; without them the spec auto-skips.
test:
	LD_LIBRARY_PATH=$(CURDIR):$$LD_LIBRARY_PATH $(GOCMD) test ./... -count=1

clean: purge
	rm -rf libparakeet.so* parakeet_capi.h package parakeet-cpp-grpc

purge:
	rm -rf sources/parakeet.cpp
