# Pinned to the HEAD of the `rocmfp4` branch on https://github.com/walcz-de/llama.cpp-ROCmFP4.
# Auto-bumped nightly by .github/workflows/bump_deps.yaml.
ROCMFP4_VERSION?=0b6afd26bfa64f51c14794b4dfbe3da8946076d3
LLAMA_REPO?=https://github.com/walcz-de/llama.cpp-ROCmFP4

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

# rocmfp4 is a llama.cpp fork carrying the ROCmFP4 / ROCmFPx weight-quantization
# formats (ggml types 100-107) for AMD RDNA3.5 APUs. Stock llama.cpp rejects those
# tensor types, which is the whole reason this backend exists as a separate image
# rather than a flag on llama-cpp.
#
# Like bonsai, the additions are model *weight* types decoded inside libllama and
# therefore transparent to the shared gRPC server: no grpc-server.cpp allow-list
# patch is needed (unlike turboquant, whose KV-cache types do need one). So we reuse
# backend/cpp/llama-cpp's grpc-server.cpp / CMakeLists.txt / Makefile verbatim and
# only swap which repo and commit the fetch step pulls.
PATCHES_DIR := $(CURRENT_MAKEFILE_DIR)/patches

define rocmfp4-build
	rm -rf $(CURRENT_MAKEFILE_DIR)/../rocmfp4-$(1)-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../rocmfp4-$(1)-build
	# Drop patches vendored for upstream llama.cpp: this fork carries them already
	# (it is based on the same pin), so re-applying them would reject.
	rm -rf $(CURRENT_MAKEFILE_DIR)/../rocmfp4-$(1)-build/patches
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../rocmfp4-$(1)-build purge
	bash $(LLAMA_CPP_DIR)/disable-score-task.sh $(CURRENT_MAKEFILE_DIR)/../rocmfp4-$(1)-build/grpc-server.cpp
	bash $(LLAMA_CPP_DIR)/disable-tts-task.sh $(CURRENT_MAKEFILE_DIR)/../rocmfp4-$(1)-build/grpc-server.cpp
	$(info $(GREEN)I rocmfp4 build info:$(1)$(RESET))
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(ROCMFP4_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../rocmfp4-$(1)-build llama.cpp
	bash $(CURRENT_MAKEFILE_DIR)/apply-patches.sh $(CURRENT_MAKEFILE_DIR)/../rocmfp4-$(1)-build/llama.cpp $(PATCHES_DIR)
	CMAKE_ARGS="$(CMAKE_ARGS) $(2)" TARGET="$(3)" \
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(ROCMFP4_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../rocmfp4-$(1)-build grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../rocmfp4-$(1)-build/grpc-server rocmfp4-$(1)
endef

rocmfp4-fallback:
	$(call rocmfp4-build,fallback,-DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

rocmfp4-grpc:
	$(call rocmfp4-build,grpc,-DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server --target ggml-rpc-server --target llama-quantize)

# Upstream renamed the RPC binary rpc-server -> ggml-rpc-server. The bonsai wrapper
# this was modelled on still uses the old name because its fork predates the rename;
# ours is based on a current tree, so it must use the new one.
rocmfp4-rpc-server: rocmfp4-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../rocmfp4-grpc-build/llama.cpp/build/bin/ggml-rpc-server rocmfp4-rpc-server

# The FP4 formats are only usable if people can produce weights, not just consume the
# single published model. The fork carries the quantization recipes and CLI arguments,
# so shipping llama-quantize next to the server turns "one model" into "any GGUF you
# already have". Built by the grpc target above, which needs no accelerator.
rocmfp4-quantize: rocmfp4-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../rocmfp4-grpc-build/llama.cpp/build/bin/llama-quantize rocmfp4-quantize

package:
	bash package.sh

purge:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../rocmfp4-*-build
	rm -rf rocmfp4-* package

clean: purge
