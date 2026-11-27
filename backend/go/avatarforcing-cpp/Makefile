# avatarforcing-cpp backend Makefile.
#
# Builds libavatarforcing.so (the flat C API of avatarforcing.cpp) from the
# upstream source at the pinned commit, then the purego gRPC server that loads
# it. The pin lives below as AVATARFORCING_VERSION so the auto-bump bot
# (.github/bump_deps.sh) can find and update it.
#
# Local development: point AVATARFORCING_SRC at an existing avatarforcing.cpp
# checkout (with its ggml submodule) to build against it instead of the pin:
#
#   make AVATARFORCING_SRC=/path/to/avatarforcing.cpp
#
# Only the CPU and CUDA builds are supported: avatarforcing.cpp patches ggml
# with a grid_sample operator that has CPU and CUDA kernels only.

AVATARFORCING_VERSION?=f2f4a21dbb5a86ac78a871ab55164fdaa9c268fb
AVATARFORCING_REPO?=https://github.com/mudler/avatarforcing.cpp
AVATARFORCING_SRC?=

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

BUILD_TYPE?=
NATIVE?=false

ifeq ($(AVATARFORCING_SRC),)
	SRC_DIR=sources/avatarforcing.cpp
else
	SRC_DIR=$(AVATARFORCING_SRC)
endif
BUILD_DIR=build

# libavatarforcing.so holds ggml as static PIC libraries and exports only the
# avatarforcing_* functions, so dlopen needs no libggml*.so next to it.
CMAKE_ARGS?=-DCMAKE_BUILD_TYPE=Release -DAVATARFORCING_BUILD_SHARED=ON \
	-DAVATARFORCING_BUILD_CLI=OFF -DAVATARFORCING_BUILD_TESTS=OFF

# The upstream CMakeLists turns GGML_NATIVE on unless it is defined; a
# portable image must not carry -march=native.
ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifneq ($(BUILD_TYPE),)
$(error avatarforcing-cpp supports only the CPU and CUDA (cublas) builds, not BUILD_TYPE=$(BUILD_TYPE))
endif

.PHONY: avatarforcing-cpp-grpc engine package build clean purge test all

all: avatarforcing-cpp-grpc

# Clone the upstream source at the pinned commit. The directory is the target,
# so make clones only when it is missing. After a version bump, run
# 'make purge && make' to fetch again.
sources/avatarforcing.cpp:
	mkdir -p sources/avatarforcing.cpp
	cd sources/avatarforcing.cpp && \
	git init -q && \
	git remote add origin $(AVATARFORCING_REPO) && \
	git fetch --depth 1 origin $(AVATARFORCING_VERSION) && \
	git checkout FETCH_HEAD && \
	git submodule update --init --recursive --depth 1 --single-branch

libavatarforcing.so: $(if $(AVATARFORCING_SRC),,sources/avatarforcing.cpp)
	cmake -B $(BUILD_DIR) -S $(SRC_DIR) $(CMAKE_ARGS)
	cmake --build $(BUILD_DIR) --config Release --target avatarforcing_shared -j$(JOBS)
	cp -fv $(BUILD_DIR)/libavatarforcing.so ./
	cp -fv $(SRC_DIR)/include/avatarforcing.h ./

# The C++ build alone, so the Dockerfile can cache it in its own layer.
engine: libavatarforcing.so

avatarforcing-cpp-grpc: libavatarforcing.so $(wildcard *.go)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o avatarforcing-cpp-grpc .

package: avatarforcing-cpp-grpc
	bash package.sh

build: package

# The unit specs need no library. The end-to-end spec needs the library, a
# GGUF, a portrait and a WAV file (see avatarforcing_test.go) and is skipped
# without them.
test:
	bash test.sh

clean: purge
	rm -rf libavatarforcing.so avatarforcing.h package avatarforcing-cpp-grpc $(BUILD_DIR)

purge:
	rm -rf sources/avatarforcing.cpp
