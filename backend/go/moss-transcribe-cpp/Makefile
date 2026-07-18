# moss-transcribe-cpp backend Makefile.
#
# Upstream pin lives below as MOSS_VERSION?=190a569c13b4b247450f2fb3b2a431244e84833e
# (.github/bump_deps.sh) can find and update it - matches the
# whisper.cpp / parakeet-cpp / ds4 convention.
#
# Local dev shortcut: if you already have an out-of-tree moss-transcribe.cpp
# build, you can symlink the .so + header into this directory and skip the
# clone/cmake steps entirely, e.g.:
#
#   ln -sf /path/to/moss-transcribe.cpp/build-shared/libmoss-transcribe.so .
#   ln -sf /path/to/moss-transcribe.cpp/include/moss_transcribe_capi.h .
#   go build -o moss-transcribe-cpp-grpc .
#
# That's what the L0 smoke test uses. The default target below does the proper
# clone-at-pin + cmake build so CI doesn't need a side-checkout.

MOSS_VERSION?=190a569c13b4b247450f2fb3b2a431244e84833e
MOSS_REPO?=https://github.com/mudler/moss-transcribe.cpp

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

BUILD_TYPE?=
NATIVE?=false

# Build ggml statically into libmoss-transcribe.so (PIC) so the shared lib is
# self-contained: dlopen needs no libggml*.so alongside it, only system libs
# (libstdc++/libgomp/libc) that the runtime image already provides. MT_SHARED
# flips ggml to a static PIC build (see the moss-transcribe.cpp CMakeLists).
CMAKE_ARGS?=-DCMAKE_BUILD_TYPE=Release -DMT_SHARED=ON -DMT_BUILD_CLI=OFF -DBUILD_SHARED_LIBS=OFF -DCMAKE_POSITION_INDEPENDENT_CODE=ON

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# moss-transcribe.cpp gates its GGML backends behind MT_GGML_* options and does
# set(GGML_<be> ${MT_GGML_<be>} CACHE BOOL "" FORCE), so a bare -DGGML_CUDA=ON is
# overwritten back to OFF and the build silently falls back to CPU. Forward the
# MT_GGML_* options instead (openblas is not gated, so -DGGML_BLAS passes through).
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DMT_GGML_CUDA=ON -DGGML_CUDA_GRAPHS=ON
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DMT_GGML_HIP=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DMT_GGML_VULKAN=ON
else ifeq ($(BUILD_TYPE),metal)
	CMAKE_ARGS+=-DMT_GGML_METAL=ON
endif

.PHONY: moss-transcribe-cpp-grpc package build clean purge test all

all: moss-transcribe-cpp-grpc

# Clone the upstream moss-transcribe.cpp source at the pinned commit. Directory
# acts as the target so make only re-clones when missing. After a MOSS_VERSION
# bump, run 'make purge && make' to refetch.
sources/moss-transcribe.cpp:
	mkdir -p sources/moss-transcribe.cpp
	cd sources/moss-transcribe.cpp && \
	git init -q && \
	git remote add origin $(MOSS_REPO) && \
	git fetch --depth 1 origin $(MOSS_VERSION) && \
	git checkout FETCH_HEAD && \
	git submodule update --init --recursive --depth 1 --single-branch

# Build the shared lib + header out-of-tree, then stage them next to the Go
# sources so purego.Dlopen("libmoss-transcribe.so") and the cgo-less build both
# pick them up.
libmoss-transcribe.so: sources/moss-transcribe.cpp
	cmake -B sources/moss-transcribe.cpp/build-shared -S sources/moss-transcribe.cpp $(CMAKE_ARGS)
	cmake --build sources/moss-transcribe.cpp/build-shared --config Release -j$(JOBS)
	cp -fv sources/moss-transcribe.cpp/build-shared/libmoss-transcribe.so* ./ 2>/dev/null || true
	cp -fv sources/moss-transcribe.cpp/build-shared/libmoss-transcribe.dylib ./ 2>/dev/null || true
	cp -fv sources/moss-transcribe.cpp/include/moss_transcribe_capi.h ./

moss-transcribe-cpp-grpc: libmoss-transcribe.so main.go gomosstranscribecpp.go segments.go
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o moss-transcribe-cpp-grpc .

package: moss-transcribe-cpp-grpc
	bash package.sh

build: package

# Test target. The model-backed smoke test is gated on
# MOSS_BACKEND_TEST_MODEL + MOSS_BACKEND_TEST_WAV; without them that spec
# auto-skips, leaving the pure-Go parser/unit tests.
test:
	LD_LIBRARY_PATH=$(CURDIR):$$LD_LIBRARY_PATH $(GOCMD) test ./... -count=1

clean: purge
	rm -rf libmoss-transcribe.so* moss_transcribe_capi.h package moss-transcribe-cpp-grpc

purge:
	rm -rf sources/moss-transcribe.cpp
