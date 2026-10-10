
# Pinned to the HEAD of master on https://github.com/fewtarius/CachyLLama.
# Auto-bumped nightly by .github/workflows/bump_deps.yaml.
CACHYLLAMA_VERSION?=cea45f4222e1eea5c2c68388d7e75904a65b6778
LLAMA_REPO?=https://github.com/fewtarius/CachyLLama

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

# cachyllama is a llama.cpp fork. Rather than duplicating grpc-server.cpp / CMakeLists.txt /
# prepare.sh we reuse the ones in backend/cpp/llama-cpp, and only swap which repo+sha the
# fetch step pulls. Each flavor target copies ../llama-cpp into a sibling ../cachyllama-<flavor>-build
# directory, then invokes llama-cpp's own build-llama-cpp-grpc-server with LLAMA_REPO/LLAMA_VERSION
# overridden to point at the fork.
# Each flavor target:
#   1. copies backend/cpp/llama-cpp/ (grpc-server.cpp + prepare.sh + CMakeLists.txt + Makefile)
#      into a sibling cachyllama-<flavor>-build directory;
#   2. clones the cachyllama fork into cachyllama-<flavor>-build/llama.cpp via the copy's
#      own `llama.cpp` target, overriding LLAMA_REPO/LLAMA_VERSION;
#   3. runs the copy's `grpc-server` target, which produces the binary we copy up as
#      cachyllama-<flavor>.
define cachyllama-build
	rm -rf $(CURRENT_MAKEFILE_DIR)/../cachyllama-$(1)-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../cachyllama-$(1)-build
	# Drop patches vendored for upstream llama.cpp; CachyLLaMA tracks upstream
	# closely but must not accidentally receive patches pinned to another SHA.
	rm -rf $(CURRENT_MAKEFILE_DIR)/../cachyllama-$(1)-build/patches
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../cachyllama-$(1)-build purge
	bash $(LLAMA_CPP_DIR)/disable-score-task.sh $(CURRENT_MAKEFILE_DIR)/../cachyllama-$(1)-build/grpc-server.cpp
	$(info $(GREEN)I cachyllama build info:$(1)$(RESET))
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(CACHYLLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../cachyllama-$(1)-build llama.cpp
	CMAKE_ARGS="$(CMAKE_ARGS) $(2)" TARGET="$(3)" \
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(CACHYLLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../cachyllama-$(1)-build grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../cachyllama-$(1)-build/grpc-server cachyllama-$(1)
endef

cachyllama-avx2:
	$(call cachyllama-build,avx2,-DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

cachyllama-avx512:
	$(call cachyllama-build,avx512,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

cachyllama-avx:
	$(call cachyllama-build,avx,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

cachyllama-fallback:
	$(call cachyllama-build,fallback,-DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

# Single-build CPU backend via ggml CPU_ALL_VARIANTS (mirrors llama-cpp-cpu-all).
# cachyllama reuses backend/cpp/llama-cpp's CMakeLists.txt (hw_grpc_proto STATIC) and
# Makefile (SHARED_LIBS make-var + EXTRA_CMAKE_ARGS), so this passes the same overrides
# through to the copied build: SHARED_LIBS=ON, the DL flags, and --target ggml (which
# pulls in the per-microarch libggml-cpu-*.so via ggml's add_dependencies). The .so set
# is collected for package.sh to bundle into package/lib.
cachyllama-cpu-all:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../cachyllama-cpu-all-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../cachyllama-cpu-all-build
	# Drop patches vendored for upstream llama.cpp; they are SHA-specific.
	rm -rf $(CURRENT_MAKEFILE_DIR)/../cachyllama-cpu-all-build/patches
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../cachyllama-cpu-all-build purge
	bash $(LLAMA_CPP_DIR)/disable-score-task.sh $(CURRENT_MAKEFILE_DIR)/../cachyllama-cpu-all-build/grpc-server.cpp
	$(info $(GREEN)I cachyllama build info:cpu-all-variants$(RESET))
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(CACHYLLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../cachyllama-cpu-all-build llama.cpp
	SHARED_LIBS=ON EXTRA_CMAKE_ARGS="-DGGML_BACKEND_DL=ON -DGGML_CPU_ALL_VARIANTS=ON" TARGET="--target grpc-server --target ggml" \
	LLAMA_REPO=$(LLAMA_REPO) LLAMA_VERSION=$(CACHYLLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../cachyllama-cpu-all-build grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../cachyllama-cpu-all-build/grpc-server cachyllama-cpu-all
	rm -rf ggml-shared-libs && mkdir -p ggml-shared-libs
	find $(CURRENT_MAKEFILE_DIR)/../cachyllama-cpu-all-build/llama.cpp/build \( -name '*.so*' -o -name '*.dylib' \) -exec cp -av {} ggml-shared-libs/ \;
	@echo "Collected ggml shared backends:" && ls -la ggml-shared-libs/

cachyllama-grpc:
	$(call cachyllama-build,grpc,-DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server --target ggml-rpc-server)

cachyllama-rpc-server: cachyllama-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../cachyllama-grpc-build/llama.cpp/build/bin/ggml-rpc-server cachyllama-rpc-server

package:
	bash package.sh

purge:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../cachyllama-*-build
	rm -rf cachyllama-* package

clean: purge
