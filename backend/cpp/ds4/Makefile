# ds4 backend Makefile.
CURDIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
DS4_DIR := $(CURDIR)ds4
BUILD_DIR := $(CURDIR)build

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

.PHONY: prepare grpc-server package clean purge test all
all: grpc-server

prepare:
	bash $(CURDIR)prepare.sh

# Build ds4's engine object files via its own Makefile, which already encodes
# the right per-platform compile flags (Objective-C/Metal on Darwin, nvcc on Linux+CUDA).
$(DS4_DIR)/ds4.o: prepare
ifeq ($(BUILD_TYPE),cublas)
	+$(MAKE) -C $(DS4_DIR) ds4.o ds4_cuda.o
else ifeq ($(UNAME_S),Darwin)
	+$(MAKE) -C $(DS4_DIR) ds4.o ds4_metal.o
else
	+$(MAKE) -C $(DS4_DIR) ds4_cpu.o
endif

grpc-server: $(DS4_DIR)/ds4.o
	mkdir -p $(BUILD_DIR)
	cd $(BUILD_DIR) && cmake $(CMAKE_ARGS) -DDS4_DIR=$(DS4_DIR) $(CURDIR) && cmake --build . --config Release -j $(JOBS)
	cp $(BUILD_DIR)/grpc-server $(CURDIR)grpc-server

package: grpc-server
	bash $(CURDIR)package.sh

test:
	@echo "ds4 backend: e2e coverage at tests/e2e-backends/ (BACKEND_BINARY mode)"

clean:
	rm -rf $(BUILD_DIR) $(CURDIR)grpc-server $(CURDIR)package
	if [ -d $(DS4_DIR) ]; then $(MAKE) -C $(DS4_DIR) clean; fi

purge: clean
	rm -rf $(DS4_DIR)
