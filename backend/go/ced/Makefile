# ced sound-classification backend Makefile.
#
# Upstream pin lives below as CED_VERSION?=db5aae02973a745722d6fbd2157cab1999106777
# and update it (matches the parakeet-cpp / whisper.cpp convention).
#
# Local dev shortcut: symlink an out-of-tree ced.cpp shared build + header and
# skip the clone/cmake steps entirely:
#   ln -sf /path/to/ced.cpp/build-shared/libced.so .
#   ln -sf /path/to/ced.cpp/include/ced_capi.h .
#   go build -o ced-grpc .

CED_VERSION?=db5aae02973a745722d6fbd2157cab1999106777
CED_REPO?=https://github.com/localai-org/ced.cpp

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

BUILD_TYPE?=
NATIVE?=false

# Static-link ggml into libced.so (PIC) so the shared lib is self-contained:
# dlopen needs no libggml*.so alongside it, only system libs the runtime image
# already provides.
CMAKE_ARGS?=-DCMAKE_BUILD_TYPE=Release -DCED_SHARED=ON -DCED_BUILD_CLI=OFF -DCED_BUILD_TESTS=OFF -DBUILD_SHARED_LIBS=OFF -DCMAKE_POSITION_INDEPENDENT_CODE=ON

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# ced.cpp gates its ggml backends behind CED_GGML_* options (set(... CACHE BOOL
# "" FORCE)), so forward those instead of a bare -DGGML_CUDA=ON.
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DCED_GGML_CUDA=ON -DGGML_CUDA_GRAPHS=ON
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DCED_GGML_HIP=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DCED_GGML_VULKAN=ON
endif

.PHONY: ced-grpc package build clean purge test all

all: ced-grpc

sources/ced.cpp:
	mkdir -p sources/ced.cpp
	cd sources/ced.cpp && \
	git init -q && \
	git remote add origin $(CED_REPO) && \
	git fetch --depth 1 origin $(CED_VERSION) && \
	git checkout FETCH_HEAD && \
	git submodule update --init --recursive --depth 1 --single-branch

libced.so: sources/ced.cpp
	cmake -B sources/ced.cpp/build-shared -S sources/ced.cpp $(CMAKE_ARGS)
	cmake --build sources/ced.cpp/build-shared --config Release -j$(JOBS)
	cp -fv sources/ced.cpp/build-shared/libced.so* ./ 2>/dev/null || true
	cp -fv sources/ced.cpp/build-shared/libced.dylib ./ 2>/dev/null || true
	cp -fv sources/ced.cpp/include/ced_capi.h ./

ced-grpc: libced.so main.go goced.go
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o ced-grpc .

package: ced-grpc
	bash package.sh

build: package

test:
	LD_LIBRARY_PATH=$(CURDIR):$$LD_LIBRARY_PATH $(GOCMD) test ./... -count=1

clean: purge
	rm -rf libced.so* ced_capi.h package ced-grpc

purge:
	rm -rf sources/ced.cpp
