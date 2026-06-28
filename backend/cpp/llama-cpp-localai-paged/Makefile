
# llama-cpp-localai-paged is LocalAI's paged-attention llama.cpp variant. It
# builds upstream llama.cpp with the LocalAI paged-attention patch series
# (patches/paged/, vendored in THIS backend) applied on top. It reuses
# backend/cpp/llama-cpp's grpc-server.cpp / CMakeLists.txt / prepare.sh / Makefile
# sources verbatim via a thin wrapper - the stock llama-cpp backend is pure
# upstream and carries NONE of the paged patches; this backend OWNS them.
#
# Pin handling (mirrors the turboquant wrapper, the precedent this is modelled
# on): the paged patch series is hand-verified bit-exact against ONE specific
# llama.cpp tip and re-exported by the manual PIN_SYNC process
# (README section 7 + .agents/llama-cpp-localai-paged-backend.md). A naive
# pin bump would move the tip out from
# under the patches and break `git apply` at build time, so this backend OWNS
# its pin (LLAMA_VERSION below) instead of inheriting the auto-bumped stock pin
# from backend/cpp/llama-cpp/Makefile. The override is forced into every copied
# build via `LLAMA_VERSION=$(LLAMA_VERSION)`. There is deliberately NO
# bump_deps.yaml entry for it: it is advanced ONLY by PIN_SYNC, never nightly.
# (turboquant CAN auto-bump because its fork branch carries the patches; the
# paged series is vendored as .patch files here, so it cannot.)
#
#   - NO patch-grpc-server.sh and NO apply-patches.sh: the shared grpc-server.cpp
#     already carries the (runtime-gated) paged option hooks, and the paged patch
#     series (patches/paged/) is applied by THIS Makefile's own apply step onto
#     the freshly cloned tree, using the same strict `git apply` method the stock
#     build uses for base patches. The stock llama-cpp Makefile applies only its
#     own (currently empty) base patches/ series, never the paged one.

# Manually pin-synced llama.cpp tip the paged patch series is verified against.
# Decoupled from the auto-bumped stock pin in backend/cpp/llama-cpp/Makefile so
# the nightly llama.cpp bump cannot silently break the vendored paged patches.
# Advance ONLY via the PIN_SYNC process (rebase patches + bit-exact gate +
# re-export), then update this value. See:
#   README section 7 + .agents/llama-cpp-localai-paged-backend.md
#
# This pin = the manual, verified sync. The signal telling you WHEN to do the
# next sync is the early-warning canary
# (.github/workflows/llama-cpp-paged-canary.yml): weekly it applies + compiles
# this patch series against the latest upstream llama.cpp tip and goes red the
# moment upstream drifts past the patches. Canary red -> run a PIN_SYNC, then
# bump this value. The canary never touches this pin; it is signal-only.
#
# HARD CONSTRAINT: keep this == the stock llama-cpp pin (backend/cpp/llama-cpp/
# Makefile). grpc-server.cpp is SHARED with the stock backend and tracks the
# stock pin; a paged pin that diverges PAST an upstream server-API refactor
# breaks the grpc-server LINK even when the patches are byte-for-byte bit-exact.
# The c299a92c bump did exactly this: patches applied + greedy-md5 bit-exact, but
# grpc-server.cpp failed to link with undefined references to stream_* server
# helpers that the refactor pulled into the headers grpc-server.cpp includes.
# Therefore a PIN_SYNC must pass the FULL grpc-server build/link on CI, not only
# the bit-exact gate. See README section 7 + .agents/llama-cpp-localai-paged-backend.md.
LLAMA_VERSION?=0ed235ea2c17a19fc8238668653946721ed136fd

CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false
ONEAPI_VARS?=/opt/intel/oneapi/setvars.sh
TARGET?=--target grpc-server
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)
ARCH?=$(shell uname -m)

CURRENT_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
LLAMA_CPP_DIR := $(CURRENT_MAKEFILE_DIR)/../llama-cpp
# OUR vendored paged-attention patch series. Owned by this backend; the stock
# llama-cpp backend no longer carries it. Applied onto each freshly cloned
# llama.cpp tree by apply-paged-patches below (strict git apply).
PAGED_PATCHES_DIR := $(CURRENT_MAKEFILE_DIR)/patches/paged

GREEN := \033[0;32m
RESET := \033[0m

# Apply OUR vendored paged-attention patch series (patches/paged/0*.patch) onto a
# freshly cloned llama.cpp tree ($(1)) using the SAME strict git-apply method the
# stock build uses for its base patches (backend/cpp/llama-cpp/Makefile `llama.cpp`
# target). Strict: any patch that no longer applies aborts the build (exit 1) -
# that is the signal to run a PIN_SYNC, never to bump the pin blindly. The series
# is owned by THIS backend, not by the now-pure stock llama-cpp backend.
define apply-paged-patches
	cd $(1) && \
	for p in $(PAGED_PATCHES_DIR)/0*.patch; do \
		[ -e "$$p" ] || continue; \
		echo "applying llama.cpp PAGED patch: $$p"; \
		git apply --verbose "$$p" || { echo "paged patch failed: $$p"; exit 1; }; \
	done
endef

# Each flavor target:
#   1. copies backend/cpp/llama-cpp/ (grpc-server.cpp + prepare.sh +
#      CMakeLists.txt + Makefile) into a sibling
#      llama-cpp-localai-paged-<flavor>-build directory;
#   2. clones OUR pinned upstream llama.cpp into that copy via the copy's own
#      `llama.cpp` target (which applies the stock base patches/ series, normally
#      empty), then applies THIS backend's paged patch series (patches/paged/)
#      onto the cloned tree with strict `git apply` (apply-paged-patches);
#   3. runs the copy's `grpc-server` target and copies the produced binary up as
#      llama-cpp-localai-paged-<flavor>.
# We clone+patch only the *copy*, never the original under backend/cpp/llama-cpp/,
# so the stock llama-cpp build stays untouched and patch-free.
define paged-build
	rm -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-$(1)-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-$(1)-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-$(1)-build purge
	$(info $(GREEN)I llama-cpp-localai-paged build info:$(1)$(RESET))
	LLAMA_VERSION=$(LLAMA_VERSION) $(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-$(1)-build llama.cpp
	$(call apply-paged-patches,$(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-$(1)-build/llama.cpp)
	CMAKE_ARGS="$(CMAKE_ARGS) $(2)" TARGET="$(3)" LLAMA_VERSION=$(LLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-$(1)-build grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-$(1)-build/grpc-server llama-cpp-localai-paged-$(1)
endef

llama-cpp-localai-paged-avx2:
	$(call paged-build,avx2,-DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

llama-cpp-localai-paged-avx512:
	$(call paged-build,avx512,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on,--target grpc-server)

llama-cpp-localai-paged-avx:
	$(call paged-build,avx,-DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

llama-cpp-localai-paged-fallback:
	$(call paged-build,fallback,-DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server)

# Single-build CPU backend via ggml CPU_ALL_VARIANTS (mirrors llama-cpp-cpu-all).
# Reuses backend/cpp/llama-cpp's CMakeLists.txt (hw_grpc_proto STATIC) and
# Makefile (SHARED_LIBS make-var + EXTRA_CMAKE_ARGS), so this passes the same
# overrides through to the copied build: SHARED_LIBS=ON, the DL flags, and
# --target ggml (which pulls in the per-microarch libggml-cpu-*.so via ggml's
# add_dependencies). The .so set is collected for package.sh to bundle into
# package/lib.
llama-cpp-localai-paged-cpu-all:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-cpu-all-build
	cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-cpu-all-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-cpu-all-build purge
	$(info $(GREEN)I llama-cpp-localai-paged build info:cpu-all-variants$(RESET))
	LLAMA_VERSION=$(LLAMA_VERSION) $(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-cpu-all-build llama.cpp
	$(call apply-paged-patches,$(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-cpu-all-build/llama.cpp)
	SHARED_LIBS=ON EXTRA_CMAKE_ARGS="-DGGML_BACKEND_DL=ON -DGGML_CPU_ALL_VARIANTS=ON" TARGET="--target grpc-server --target ggml" LLAMA_VERSION=$(LLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-cpu-all-build grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-cpu-all-build/grpc-server llama-cpp-localai-paged-cpu-all
	rm -rf ggml-shared-libs && mkdir -p ggml-shared-libs
	find $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-cpu-all-build/llama.cpp/build \( -name '*.so*' -o -name '*.dylib' \) -exec cp -av {} ggml-shared-libs/ \;
	@echo "Collected ggml shared backends:" && ls -la ggml-shared-libs/

llama-cpp-localai-paged-grpc:
	$(call paged-build,grpc,-DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off,--target grpc-server --target ggml-rpc-server)

llama-cpp-localai-paged-rpc-server: llama-cpp-localai-paged-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-grpc-build/llama.cpp/build/bin/ggml-rpc-server llama-cpp-localai-paged-rpc-server

package:
	bash package.sh

purge:
	rm -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp-localai-paged-*-build
	rm -rf llama-cpp-localai-paged-* package

clean: purge
