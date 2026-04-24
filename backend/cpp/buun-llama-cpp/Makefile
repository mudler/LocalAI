
# Pinned to the HEAD of master on https://github.com/spiritbuun/buun-llama-cpp.
# Auto-bumped nightly by .github/workflows/bump_deps.yaml.
BUUN_LLAMA_VERSION?=22464d0848b87c5d56b52fdf6af2e5da46bf803e
LLAMA_REPO?=https://github.com/spiritbuun/buun-llama-cpp

CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false
ONEAPI_VARS?=/opt/intel/oneapi/setvars.sh
TARGET?=--target grpc-server
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)
ARCH?=$(shell uname -m)

CURRENT_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
LLAMA_CPP_DIR := $(CURRENT_MAKEFILE_DIR)/../llama-cpp

GREEN := \033[0;32m
RESET := \033[0m

# buun-llama-cpp is a llama.cpp fork-of-a-fork (spiritbuun/buun-llama-cpp forked
# TheTom/llama-cpp-turboquant, which itself forked ggml-org/llama.cpp). Rather
# than duplicating grpc-server.cpp / CMakeLists.txt / prepare.sh we reuse the
# ones in backend/cpp/llama-cpp, and only swap which repo+sha the fetch step
# pulls. Each flavor target copies ../llama-cpp into a sibling
# ../buun-llama-cpp-<flavor>-build directory, then invokes llama-cpp's own
# build-llama-cpp-grpc-server with LLAMA_REPO/LLAMA_VERSION overridden to point
# at the fork.
PATCHES_DIR := $(CURRENT_MAKEFILE_DIR)/patches

# Each flavor target:
#   1. copies backend/cpp/llama-cpp/ (grpc-server.cpp + prepare.sh + CMakeLists.txt + Makefile)
#      into a sibling buun-llama-cpp-<flavor>-build directory;
#   2. clones the buun fork into buun-llama-cpp-<flavor>-build/llama.cpp via the
#      copy's own `llama.cpp` target, overriding LLAMA_REPO/LLAMA_VERSION;
#   3. applies patches from backend/cpp/buun-llama-cpp/patches/ to the cloned
#      fork sources (for backporting upstream commits the fork hasn't pulled);
#   4. runs the copy's `grpc-server` target, which produces the binary we copy
#      up as buun-llama-cpp-<flavor>.
define buun-llama-cpp-build
	rm -rf $(CURRENT_MAKEFILE_DIR)/../buun-llama-cpp-$(1)-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../buun-llama-cpp-$(1)-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../buun-llama-cpp-$(1)-build purge
	# Augment the copied grpc-server.cpp's KV-cache allow-list with the
	# fork's turbo2/turbo3/turbo4/turbo2_tcq/turbo3_tcq types and wire up the
	# DFlash-specific option handlers (tree_budget / draft_topk). We patch the
	# *copy*, never the original under backend/cpp/llama-cpp/, so the stock
	# llama-cpp build stays compiling against vanilla upstream.
	bash $(CURRENT_MAKEFILE_DIR)/patch-grpc-server.sh $(CURRENT_MAKEFILE_DIR)/../buun-llama-cpp-$(1)-build/grpc-server.cpp
	$(info $(GREEN)I buun-llama-cpp build info:$(1)$(RESET))
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(BUUN_LLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../buun-llama-cpp-$(1)-build llama.cpp
	bash $(CURRENT_MAKEFILE_DIR)/apply-patches.sh $(CURRENT_MAKEFILE_DIR)/../buun-llama-cpp-$(1)-build/llama.cpp $(PATCHES_DIR)
	CMAKE_ARGS="$(CMAKE_ARGS) $(2)" TARGET="$(3)" \
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(BUUN_LLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../buun-llama-cpp-$(1)-build grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../buun-llama-cpp-$(1)-build/grpc-server buun-llama-cpp-$(1)
endef

buun-llama-cpp-avx2:
	$(call buun-llama-cpp-build,avx2,-DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

buun-llama-cpp-avx512:
	$(call buun-llama-cpp-build,avx512,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

buun-llama-cpp-avx:
	$(call buun-llama-cpp-build,avx,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

buun-llama-cpp-fallback:
	$(call buun-llama-cpp-build,fallback,-DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

buun-llama-cpp-grpc:
	$(call buun-llama-cpp-build,grpc,-DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server --target rpc-server)

buun-llama-cpp-rpc-server: buun-llama-cpp-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../buun-llama-cpp-grpc-build/llama.cpp/build/bin/rpc-server buun-llama-cpp-rpc-server

package:
	bash package.sh

purge:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../buun-llama-cpp-*-build
	rm -rf buun-llama-cpp-* package

clean: purge
