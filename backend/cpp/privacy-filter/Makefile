# privacy-filter backend Makefile.
#
# Wraps the standalone privacy-filter.cpp GGML engine (the openai-privacy-filter
# PII/NER token classifier) as a LocalAI gRPC backend. The engine source is
# fetched at the pin below — .github/workflows/bump_deps.yaml finds and updates
# PRIVACY_FILTER_VERSION, matching the llama-cpp / ds4 convention.
#
# Local development: point at a working checkout instead of cloning, e.g.
#   make PRIVACY_FILTER_SRC=$HOME/c/privacy-filter.cpp grpc-server

PRIVACY_FILTER_VERSION?=3b87bf8793f440a20fe8b2dcd00f6f27e3bfd69d
PRIVACY_FILTER_REPO?=https://github.com/localai-org/privacy-filter.cpp
PRIVACY_FILTER_SRC?=

CURRENT_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
BUILD_DIR := build

BUILD_TYPE ?=
NATIVE ?= false
JOBS ?= $(shell nproc 2>/dev/null || echo 4)

CMAKE_ARGS ?= -DCMAKE_BUILD_TYPE=Release

# GPU backends; the default (cpu) needs no extra flags. 'cublas' is LocalAI's
# name for the CUDA build (matches llama-cpp / ds4), mapping to the engine's
# GGML_CUDA path; 'vulkan' selects the ggml Vulkan backend; 'hipblas' the ggml
# HIP backend (ROCm).
ifeq ($(BUILD_TYPE),cublas)
    CMAKE_ARGS += -DPF_CUDA=ON
endif
ifeq ($(BUILD_TYPE),vulkan)
    CMAKE_ARGS += -DPF_VULKAN=ON
endif
# 'hipblas' builds the ggml HIP backend for AMD GPUs. The engine has no PF_HIP
# switch; GGML_HIP reaches ggml directly through add_subdirectory(ggml). Use the
# ROCm clang and forward the gfx targets, otherwise ggml falls back to a CPU-only
# build without an error (the same trap as whisper/parakeet-cpp).
ifeq ($(BUILD_TYPE),hipblas)
    ROCM_HOME ?= /opt/rocm
    ROCM_PATH ?= /opt/rocm
    export CXX=$(ROCM_HOME)/llvm/bin/clang++
    export CC=$(ROCM_HOME)/llvm/bin/clang
    AMDGPU_TARGETS ?= gfx908,gfx90a,gfx942,gfx950,gfx1030,gfx1100,gfx1101,gfx1102,gfx1103,gfx1150,gfx1151,gfx1152,gfx1153,gfx1200,gfx1201
    CMAKE_ARGS += -DGGML_HIP=ON -DAMDGPU_TARGETS=$(AMDGPU_TARGETS) -DGPU_TARGETS=$(AMDGPU_TARGETS)
endif

# Portable binaries for distribution: disable -march=native unless asked.
ifneq ($(NATIVE),true)
    CMAKE_ARGS += -DGGML_NATIVE=OFF
endif

.PHONY: grpc-server package clean purge test all
all: grpc-server

# Provide the engine sources at ./privacy-filter.cpp. With PRIVACY_FILTER_SRC
# set we symlink a local checkout (instant, no network); otherwise we clone the
# pinned commit and its ggml submodule. The directory/symlink is the target, so
# make only does this once — run 'make purge && make' to refetch after a bump.
privacy-filter.cpp:
ifneq ($(PRIVACY_FILTER_SRC),)
	ln -sfn $(abspath $(PRIVACY_FILTER_SRC)) privacy-filter.cpp
else
	mkdir -p privacy-filter.cpp
	cd privacy-filter.cpp && \
	git init -q && \
	git remote add origin $(PRIVACY_FILTER_REPO) && \
	git fetch --depth 1 origin $(PRIVACY_FILTER_VERSION) && \
	git checkout FETCH_HEAD && \
	git submodule update --init --recursive --depth 1
endif

grpc-server: privacy-filter.cpp
	@echo "Building privacy-filter grpc-server ($(BUILD_TYPE)) with $(CMAKE_ARGS)"
	mkdir -p $(BUILD_DIR)
	cd $(BUILD_DIR) && cmake $(CMAKE_ARGS) $(CURRENT_MAKEFILE_DIR) && cmake --build . --config Release -j $(JOBS)
	cp $(BUILD_DIR)/grpc-server grpc-server

package: grpc-server
	bash package.sh

test:
	@echo "privacy-filter backend: parity/regression coverage lives in the engine repo"

clean:
	rm -rf $(BUILD_DIR) grpc-server package

# 'privacy-filter.cpp' may be a symlink (PRIVACY_FILTER_SRC) — rm without a
# trailing slash removes the link, never the linked-to checkout.
purge: clean
	rm -rf privacy-filter.cpp
