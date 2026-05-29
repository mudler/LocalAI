# parakeet-cpp backend Makefile.
#
# Upstream pin lives below as PARAKEET_VERSION?=46ed7fd038cbf1ad6e04bb0436c126dc360e20c5
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

PARAKEET_VERSION?=46ed7fd038cbf1ad6e04bb0436c126dc360e20c5
PARAKEET_REPO?=https://github.com/mudler/parakeet.cpp

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

BUILD_TYPE?=
NATIVE?=false

CMAKE_ARGS?=-DCMAKE_BUILD_TYPE=Release -DPARAKEET_SHARED=ON -DPARAKEET_BUILD_CLI=OFF

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DGGML_HIP=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=ON
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
	git checkout FETCH_HEAD

# Build the shared lib + header out-of-tree, then stage them next to the
# Go sources so purego.Dlopen("libparakeet.so") and the cgo-less build
# both pick them up.
libparakeet.so: sources/parakeet.cpp
	cmake -B sources/parakeet.cpp/build-shared -S sources/parakeet.cpp $(CMAKE_ARGS)
	cmake --build sources/parakeet.cpp/build-shared --config Release -j$(JOBS)
	cp -fv sources/parakeet.cpp/build-shared/libparakeet.so* ./ 2>/dev/null || true
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
