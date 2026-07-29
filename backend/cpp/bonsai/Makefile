
# Pinned to the HEAD of the `prism` branch on https://github.com/PrismML-Eng/llama.cpp.
# Auto-bumped nightly by .github/workflows/bump_deps.yaml.
BONSAI_VERSION?=7529fdaaf99ffdc5ca71ace9c7409a56b27ad92f
LLAMA_REPO?=https://github.com/PrismML-Eng/llama.cpp

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

# bonsai is a llama.cpp fork (PrismML) adding the Q1_0 (1-bit) and Q2_0 (ternary)
# weight-quantization kernels that the Bonsai / Ternary-Bonsai models ship in. Rather
# than duplicating grpc-server.cpp / CMakeLists.txt / prepare.sh we reuse the ones in
# backend/cpp/llama-cpp, and only swap which repo+sha the fetch step pulls. Each flavor
# target copies ../llama-cpp into a sibling ../bonsai-<flavor>-build directory, then
# invokes llama-cpp's own build with LLAMA_REPO/LLAMA_VERSION overridden to point at the
# fork.
#
# The Q1_0/Q2_0 additions are model *weight* types decoded inside libllama, transparent
# to the reused gRPC server, so (unlike turboquant's KV-cache types) no grpc-server.cpp
# allow-list patch is needed. The fork branched from upstream before a few API changes
# the shared grpc-server.cpp depends on; those are carried as patch files under
# backend/cpp/bonsai/patches/ and applied to the cloned fork by apply-patches.sh.
PATCHES_DIR := $(CURRENT_MAKEFILE_DIR)/patches

define bonsai-build
	rm -rf $(CURRENT_MAKEFILE_DIR)/../bonsai-$(1)-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../bonsai-$(1)-build
	# Drop patches vendored for upstream llama.cpp: the fork tree diverges, so
	# they reject there. Fork-specific patches live in backend/cpp/bonsai/patches/
	# and are applied by apply-patches.sh below.
	rm -rf $(CURRENT_MAKEFILE_DIR)/../bonsai-$(1)-build/patches
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../bonsai-$(1)-build purge
	bash $(LLAMA_CPP_DIR)/disable-score-task.sh $(CURRENT_MAKEFILE_DIR)/../bonsai-$(1)-build/grpc-server.cpp
	$(info $(GREEN)I bonsai build info:$(1)$(RESET))
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(BONSAI_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../bonsai-$(1)-build llama.cpp
	bash $(CURRENT_MAKEFILE_DIR)/apply-patches.sh $(CURRENT_MAKEFILE_DIR)/../bonsai-$(1)-build/llama.cpp $(PATCHES_DIR)
	CMAKE_ARGS="$(CMAKE_ARGS) $(2)" TARGET="$(3)" \
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(BONSAI_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../bonsai-$(1)-build grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../bonsai-$(1)-build/grpc-server bonsai-$(1)
endef

bonsai-avx2:
	$(call bonsai-build,avx2,-DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

bonsai-avx512:
	$(call bonsai-build,avx512,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

bonsai-avx:
	$(call bonsai-build,avx,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

bonsai-fallback:
	$(call bonsai-build,fallback,-DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

# Single-build CPU backend via ggml CPU_ALL_VARIANTS (mirrors llama-cpp-cpu-all).
# bonsai reuses backend/cpp/llama-cpp's CMakeLists.txt (hw_grpc_proto STATIC) and
# Makefile (SHARED_LIBS make-var + EXTRA_CMAKE_ARGS), so this passes the same overrides
# through to the copied build: SHARED_LIBS=ON, the DL flags, and --target ggml (which
# pulls in the per-microarch libggml-cpu-*.so via ggml's add_dependencies). The .so set
# is collected for package.sh to bundle into package/lib.
bonsai-cpu-all:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../bonsai-cpu-all-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../bonsai-cpu-all-build
	# Drop patches vendored for upstream llama.cpp: the fork tree diverges, so
	# they reject there. Fork-specific patches live in backend/cpp/bonsai/patches/
	# and are applied by apply-patches.sh below.
	rm -rf $(CURRENT_MAKEFILE_DIR)/../bonsai-cpu-all-build/patches
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../bonsai-cpu-all-build purge
	bash $(LLAMA_CPP_DIR)/disable-score-task.sh $(CURRENT_MAKEFILE_DIR)/../bonsai-cpu-all-build/grpc-server.cpp
	$(info $(GREEN)I bonsai build info:cpu-all-variants$(RESET))
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(BONSAI_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../bonsai-cpu-all-build llama.cpp
	bash $(CURRENT_MAKEFILE_DIR)/apply-patches.sh $(CURRENT_MAKEFILE_DIR)/../bonsai-cpu-all-build/llama.cpp $(PATCHES_DIR)
	SHARED_LIBS=ON EXTRA_CMAKE_ARGS="-DGGML_BACKEND_DL=ON -DGGML_CPU_ALL_VARIANTS=ON" TARGET="--target grpc-server --target ggml" \
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(BONSAI_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../bonsai-cpu-all-build grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../bonsai-cpu-all-build/grpc-server bonsai-cpu-all
	rm -rf ggml-shared-libs && mkdir -p ggml-shared-libs
	find $(CURRENT_MAKEFILE_DIR)/../bonsai-cpu-all-build/llama.cpp/build \( -name '*.so*' -o -name '*.dylib' \) -exec cp -av {} ggml-shared-libs/ \;
	@echo "Collected ggml shared backends:" && ls -la ggml-shared-libs/

bonsai-grpc:
	$(call bonsai-build,grpc,-DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server --target rpc-server)

bonsai-rpc-server: bonsai-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../bonsai-grpc-build/llama.cpp/build/bin/rpc-server bonsai-rpc-server

package:
	bash package.sh

purge:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../bonsai-*-build
	rm -rf bonsai-* package

clean: purge
