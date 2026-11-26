
# Pinned to a commit of the `olala-master` branch on https://github.com/OVHai-LLM/llama.cpp.
# Bumped by hand, it is NOT in .github/workflows/bump_deps.yaml. See README.md.
OLALA_VERSION?=bd20ca9b471fdcb3f03b75f82d3cf575414bc573
LLAMA_REPO?=https://github.com/OVHai-LLM/llama.cpp

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

# olala is a llama.cpp fork (OVHai LLM, MIT) that adds the `olala` architecture
# (Mamba3 hybrid MoE) used by the Olala models: new ggml ops, KV/recurrent state
# handling and CPU, CUDA and Metal kernels. Upstream llama.cpp cannot load these
# models. Rather than duplicating grpc-server.cpp / CMakeLists.txt / prepare.sh we
# reuse the ones in backend/cpp/llama-cpp, and only swap which repo+sha the fetch
# step pulls. Each flavor target copies ../llama-cpp into a sibling
# ../olala-<flavor>-build directory, then invokes llama-cpp's own build with
# LLAMA_REPO/LLAMA_VERSION overridden to point at the fork.
#
# The fork branched from upstream before API changes the shared grpc-server.cpp
# depends on; those are carried as patch files under backend/cpp/olala/patches/
# and applied to the cloned fork by apply-patches.sh.
# The x86 CPU weight repack is off for every flavor of this backend.
OLALA_CMAKE_ARGS := -DGGML_CPU_REPACK=OFF
PATCHES_DIR := $(CURRENT_MAKEFILE_DIR)/patches

define olala-build
	rm -rf $(CURRENT_MAKEFILE_DIR)/../olala-$(1)-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../olala-$(1)-build
	# Drop patches vendored for upstream llama.cpp: the fork tree diverges, so
	# they reject there. Fork-specific patches live in backend/cpp/olala/patches/
	# and are applied by apply-patches.sh below.
	rm -rf $(CURRENT_MAKEFILE_DIR)/../olala-$(1)-build/patches
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../olala-$(1)-build purge
	bash $(LLAMA_CPP_DIR)/disable-score-task.sh $(CURRENT_MAKEFILE_DIR)/../olala-$(1)-build/grpc-server.cpp
	bash $(LLAMA_CPP_DIR)/disable-tts-task.sh $(CURRENT_MAKEFILE_DIR)/../olala-$(1)-build/grpc-server.cpp
	$(info $(GREEN)I olala build info:$(1)$(RESET))
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(OLALA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../olala-$(1)-build llama.cpp
	bash $(CURRENT_MAKEFILE_DIR)/apply-patches.sh $(CURRENT_MAKEFILE_DIR)/../olala-$(1)-build/llama.cpp $(PATCHES_DIR)
	CMAKE_ARGS="$(CMAKE_ARGS) $(OLALA_CMAKE_ARGS) $(2)" TARGET="$(3)" \
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(OLALA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../olala-$(1)-build grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../olala-$(1)-build/grpc-server olala-$(1)
endef

olala-avx2:
	$(call olala-build,avx2,-DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

olala-avx512:
	$(call olala-build,avx512,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

olala-avx:
	$(call olala-build,avx,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

olala-fallback:
	$(call olala-build,fallback,-DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

# Single-build CPU backend via ggml CPU_ALL_VARIANTS (mirrors llama-cpp-cpu-all).
# olala reuses backend/cpp/llama-cpp's CMakeLists.txt (hw_grpc_proto STATIC) and
# Makefile (SHARED_LIBS make-var + EXTRA_CMAKE_ARGS), so this passes the same overrides
# through to the copied build: SHARED_LIBS=ON, the DL flags, and --target ggml (which
# pulls in the per-microarch libggml-cpu-*.so via ggml's add_dependencies). The .so set
# is collected for package.sh to bundle into package/lib.
# CPU_VARIANTS_DIR (absolute path) holds libggml-cpu-*.so built from this fork's pin by
# .docker/llama-cpp-cpu-variants.sh. When set, the tree here is built with -DGGML_CPU=OFF
# (loader and accelerator backend only) and the variants are copied into ggml-shared-libs.
# Unset, the variants are built in tree. The stage reads OLALA_CMAKE_ARGS from this file,
# so the flags that shape the CPU kernels stay defined in one place.
CPU_VARIANTS_DIR?=

# CPU_ALL_RPC=ON builds the RPC backend and ggml-rpc-server in the cpu-all tree too and
# ships them as olala-grpc and olala-rpc-server, so the GPU backend is compiled once
# instead of a second time for the static olala-grpc tree. See CPU_ALL_RPC in
# backend/cpp/llama-cpp/Makefile.
CPU_ALL_RPC?=OFF
ifeq ($(CPU_ALL_RPC),ON)
CPU_ALL_RPC_CMAKE_ARGS:=-DGGML_RPC=ON
CPU_ALL_RPC_TARGET:=--target ggml-rpc-server
endif

olala-cpu-all:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build
	# Drop patches vendored for upstream llama.cpp: the fork tree diverges, so
	# they reject there. Fork-specific patches live in backend/cpp/olala/patches/
	# and are applied by apply-patches.sh below.
	rm -rf $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build/patches
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build purge
	bash $(LLAMA_CPP_DIR)/disable-score-task.sh $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build/grpc-server.cpp
	bash $(LLAMA_CPP_DIR)/disable-tts-task.sh $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build/grpc-server.cpp
	$(info $(GREEN)I olala build info:cpu-all-variants$(RESET))
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(OLALA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build llama.cpp
	bash $(CURRENT_MAKEFILE_DIR)/apply-patches.sh $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build/llama.cpp $(PATCHES_DIR)
ifneq ($(strip $(CPU_VARIANTS_DIR)),)
	test "$$(cat $(CPU_VARIANTS_DIR)/LLAMA_VERSION)" = "$(OLALA_VERSION)" || { echo "CPU variants in $(CPU_VARIANTS_DIR) were built for another commit than $(OLALA_VERSION)" >&2; exit 1; }
	SHARED_LIBS=ON EXTRA_CMAKE_ARGS="$(OLALA_CMAKE_ARGS) -DGGML_BACKEND_DL=ON -DGGML_CPU=OFF $(CPU_ALL_RPC_CMAKE_ARGS)" TARGET="--target grpc-server --target ggml $(CPU_ALL_RPC_TARGET)" \
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(OLALA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build grpc-server
else
	SHARED_LIBS=ON EXTRA_CMAKE_ARGS="$(OLALA_CMAKE_ARGS) -DGGML_BACKEND_DL=ON -DGGML_CPU_ALL_VARIANTS=ON $(CPU_ALL_RPC_CMAKE_ARGS)" TARGET="--target grpc-server --target ggml $(CPU_ALL_RPC_TARGET)" \
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(OLALA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build grpc-server
endif
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build/grpc-server olala-cpu-all
ifeq ($(CPU_ALL_RPC),ON)
	cp -fv $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build/grpc-server olala-grpc
	cp -fv $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build/llama.cpp/build/bin/ggml-rpc-server olala-rpc-server
endif
	rm -rf ggml-shared-libs && mkdir -p ggml-shared-libs
	find $(CURRENT_MAKEFILE_DIR)/../olala-cpu-all-build/llama.cpp/build \( -name '*.so*' -o -name '*.dylib' \) -exec cp -av {} ggml-shared-libs/ \;
ifneq ($(strip $(CPU_VARIANTS_DIR)),)
	cp -av $(CPU_VARIANTS_DIR)/libggml-cpu-*.so ggml-shared-libs/
endif
	@echo "Collected ggml shared backends:" && ls -la ggml-shared-libs/

olala-grpc:
	$(call olala-build,grpc,-DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server --target ggml-rpc-server)

olala-rpc-server: olala-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../olala-grpc-build/llama.cpp/build/bin/ggml-rpc-server olala-rpc-server

package:
	bash package.sh

purge:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../olala-*-build
	rm -rf olala-* package

clean: purge
