# ds4 backend Makefile.
#
# Upstream pin lives below as DS4_VERSION?=c9dd9499bfa57c1bbfbb4446eff963330ab5329b
# (.github/bump_deps.sh) can find and update it - matches the
# llama-cpp / ik-llama-cpp / turboquant convention.

DS4_VERSION?=c9dd9499bfa57c1bbfbb4446eff963330ab5329b
DS4_REPO?=https://github.com/antirez/ds4

CURRENT_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
BUILD_DIR := build

BUILD_TYPE ?=
NATIVE ?= false
JOBS ?= $(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

UNAME_S := $(shell uname -s)

CMAKE_ARGS ?= -DCMAKE_BUILD_TYPE=Release

ifeq ($(BUILD_TYPE),cublas)
    CMAKE_ARGS += -DDS4_GPU=cuda
    DS4_OBJ_TARGET := ds4.o ds4_cuda.o
else ifeq ($(UNAME_S),Darwin)
    CMAKE_ARGS += -DDS4_GPU=metal
    DS4_OBJ_TARGET := ds4.o ds4_metal.o
else
    # CPU reference path (Linux only - macOS CPU path is broken by VM bug per ds4 README).
    CMAKE_ARGS += -DDS4_GPU=cpu
    DS4_OBJ_TARGET := ds4_cpu.o
endif

ifneq ($(NATIVE),true)
    CMAKE_ARGS += -DDS4_NATIVE=OFF
endif

.PHONY: grpc-server package clean purge test all
all: grpc-server

# Clone the upstream ds4 source at the pinned commit. Directory acts as the
# target so make only re-clones when missing. After a DS4_VERSION bump,
# run 'make purge && make' to refetch (or rely on CI's clean build).
ds4:
	mkdir -p ds4
	cd ds4 && \
	git init -q && \
	git remote add origin $(DS4_REPO) && \
	git fetch --depth 1 origin $(DS4_VERSION) && \
	git checkout FETCH_HEAD

# Build ds4's engine object files via its own Makefile, which already encodes
# the right per-platform compile flags (Objective-C/Metal on Darwin, nvcc on Linux+CUDA).
ds4/ds4.o: ds4
ifeq ($(BUILD_TYPE),cublas)
	+$(MAKE) -C ds4 ds4.o ds4_cuda.o
else ifeq ($(UNAME_S),Darwin)
	+$(MAKE) -C ds4 ds4.o ds4_metal.o
else
	+$(MAKE) -C ds4 ds4_cpu.o
endif

grpc-server: ds4/ds4.o
	mkdir -p $(BUILD_DIR)
	cd $(BUILD_DIR) && cmake $(CMAKE_ARGS) $(CURRENT_MAKEFILE_DIR) && cmake --build . --config Release -j $(JOBS)
	cp $(BUILD_DIR)/grpc-server grpc-server

package: grpc-server
	bash package.sh

test:
	@echo "ds4 backend: e2e coverage at tests/e2e-backends/ (BACKEND_BINARY mode)"

clean:
	rm -rf $(BUILD_DIR) grpc-server package
	if [ -d ds4 ]; then $(MAKE) -C ds4 clean; fi

purge: clean
	rm -rf ds4
