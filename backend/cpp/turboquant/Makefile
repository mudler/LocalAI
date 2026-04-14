
# Pinned to the HEAD of feature/turboquant-kv-cache on https://github.com/TheTom/llama-cpp-turboquant.
# Auto-bumped nightly by .github/workflows/bump_deps.yaml.
TURBOQUANT_VERSION?=8590cbff961dbaf1d3a9793fd11d402e248869b9
LLAMA_REPO?=https://github.com/TheTom/llama-cpp-turboquant

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

# turboquant is a llama.cpp fork. Rather than duplicating grpc-server.cpp / CMakeLists.txt /
# prepare.sh we reuse the ones in backend/cpp/llama-cpp, and only swap which repo+sha the
# fetch step pulls. Each flavor target copies ../llama-cpp into a sibling ../turboquant-<flavor>-build
# directory, then invokes llama-cpp's own build-llama-cpp-grpc-server with LLAMA_REPO/LLAMA_VERSION
# overridden to point at the fork.
define turboquant-build
	rm -rf $(CURRENT_MAKEFILE_DIR)/../turboquant-$(1)-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../turboquant-$(1)-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../turboquant-$(1)-build purge
	$(info $(GREEN)I turboquant build info:$(1)$(RESET))
	CMAKE_ARGS="$(CMAKE_ARGS) $(2)" TARGET="$(3)" \
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(TURBOQUANT_VERSION) \
	$(MAKE) -C $(LLAMA_CPP_DIR) VARIANT="turboquant-$(1)-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../turboquant-$(1)-build/grpc-server turboquant-$(1)
endef

turboquant-avx2:
	$(call turboquant-build,avx2,-DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

turboquant-avx512:
	$(call turboquant-build,avx512,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

turboquant-avx:
	$(call turboquant-build,avx,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

turboquant-fallback:
	$(call turboquant-build,fallback,-DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

turboquant-grpc:
	$(call turboquant-build,grpc,-DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server --target rpc-server)

turboquant-rpc-server: turboquant-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../turboquant-grpc-build/llama.cpp/build/bin/rpc-server turboquant-rpc-server

package:
	bash package.sh

purge:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../turboquant-*-build
	rm -rf turboquant-* package

clean: purge
