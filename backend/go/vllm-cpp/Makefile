CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
# nproc doesn't exist on the macOS runners: an empty JOBS turns `-j$(JOBS)`
# into bare `-j` (unlimited clang jobs), which swap-thrashes the 3-core Mac
# until the 6h GHA timeout. Fall back to sysctl there, then to a constant.
JOBS?=$(shell nproc --ignore=1 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

# vllm.cpp version
VLLM_CPP_REPO?=https://github.com/mudler/vllm.cpp
VLLM_CPP_VERSION?=76f2a6d84e41cc9a593146a9a958f63645542731

# MLX GEMM provider (darwin/metal only; see the metal branch below for why).
# Consumed as the prebuilt pip wheel: building MLX from source needs `xcrun
# metal`, i.e. a full Xcode the macOS runners do not have, while the wheel ships
# include/, lib/libmlx.dylib and the compiled mlx.metallib ready to link.
#
# DEFAULT ON, but ONLY because VLLM_CPP_VERSION above is pinned at or past
# vllm.cpp 89c46aeb, which SHAPE-GATES the provider to prefill. The ordering is
# load-bearing, not incidental:
#
#   pin >= 89c46aeb, MLX on   -> 99.1% of MLX-LM  (gated: prefill only)
#   pin <  89c46aeb, MLX on   -> ~51%             (ungated: it also takes decode)
#
# MLX's steel GEMM wins prefill (537 ms TTFT against 602) and loses decode badly,
# because the provider pays an mx::eval sync plus an output memcpy per call and
# decode makes ~112 calls per TOKEN. Ungated it does both; gated it does only the
# good half. So if this pin is ever moved BACKWARDS, this default must go with it.
VLLM_CPP_MLX?=on
MLX_VERSION?=0.29.4
MLX_VENV?=$(abspath ./mlx-venv)
# Resolved lazily (recursive `=`, not `:=`): the glob only matches once the venv
# target has run, and the interpreter version in the path varies per runner.
MLX_ROOT=$(shell echo $(MLX_VENV)/lib/python*/site-packages/mlx)

# The backend consumes only the stable C ABI (libvllm + include/vllm.h), so the
# server, examples and tests of the engine are never built here.
CMAKE_ARGS+=-DVLLM_CPP_SERVER=OFF -DVLLM_CPP_BUILD_TESTS=OFF -DVLLM_CPP_BUILD_EXAMPLES=OFF
CMAKE_ARGS+=-DCMAKE_BUILD_TYPE=Release

# vllm.cpp sets no global -march: SIMD tiers are per-file with runtime dispatch,
# so ONE portable library serves every CPU of the target arch (unlike the
# ggml-based backends and their avx/avx2/avx512 variant builds).
UNAME_M := $(shell uname -m)

ifeq ($(BUILD_TYPE),cublas)
	# Every CUDA architecture upstream builds that the platform can actually
	# host, split by where the silicon exists: Jetson (87 Orin, 110 Thor) is
	# arm64-only, desktop 120a is amd64-only, and 90a/100a appear on both
	# because of the SBSA parts (GH200, GB200).
	#
	# This deliberately matches vllm.cpp's own release archive rather than
	# narrowing to the boxes we benchmark on. A narrower list does not degrade
	# on an unlisted card, it dies at the first request with "no kernel image
	# is available for execution on the device", long after `backends install`
	# reported success -- so an arch we merely lack numbers for still belongs
	# in the binary.
	#
	# Triton-AOT stays ON for both. A fat build is supported on the BUILDER
	# path: it embeds every vendored cubin tree (sm_80/86/89/90a/100a/121a) and
	# selects by exact SM at runtime, so the arches with no tree (87, 103a,
	# 110, 120a) take the portable CUDA kernels and can never load a
	# neighbouring cubin. Only maintainer REGEN needs a single pinned arch.
	# See vllm.cpp cmake/TritonAOT.cmake `_triton_aot_arch_names`.
	#
	# CUDA builds REQUIRE the CUDA 13 toolchain: 12.x nvcc lacks compute_121a
	# (GB10) and its ptxas rejects the sm_120a NVFP4 MMA kernels ("Vector type
	# too large"), so no cuda-12 variant is shipped.
	ifeq ($(CUDA_MAJOR_VERSION),12)
    $(error vllm.cpp needs the CUDA 13 toolchain: CUDA 12.x cannot compile the Blackwell fp4 kernels)
	endif
	ifeq ($(UNAME_M),x86_64)
		CMAKE_ARGS+=-DVLLM_CPP_CUDA=ON "-DVLLM_CPP_CUDA_ARCHITECTURES=80;86;89;90a;100a;103a;120a;121a" -DVLLM_CPP_TRITON=ON
	else
		CMAKE_ARGS+=-DVLLM_CPP_CUDA=ON "-DVLLM_CPP_CUDA_ARCHITECTURES=87;90a;100a;110;121a" -DVLLM_CPP_TRITON=ON
	endif
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DVLLM_CPP_VULKAN=ON -DVLLM_CPP_CUDA=OFF
else ifeq ($(BUILD_TYPE),metal)
	CMAKE_ARGS+=-DVLLM_CPP_METAL=ON
	# The optional MLX GEMM provider. vllm.cpp keeps it OFF by default because it
	# is a ~19 MB libmlx.dylib plus a ~105 MB mlx.metallib, and upstream's
	# position is that it must earn that cost by measurement. It does, on the
	# only hardware this build targets: measured on an Apple M4 against the
	# native MSL GEMM in the SAME binary (arms toggled by
	# VT_OP_PROVIDER_DISABLE=mlx), Qwen3-1.7B-bf16 p=512 g=128, it is 1.5x to
	# 2.2x aggregate throughput and 2x to 3x faster TTFT, at equal peak memory
	# and bit-identical output on every parity shape. See vllm.cpp
	# docs/BENCHMARKS.md "MLX GEMM provider A/B on Apple M4".
	#
	# MLX delegates the dense GEMM ONLY: kPagedAttention stays vllm.cpp's own
	# kernel, because MLX has no paged-KV primitive at all.
	#
	# Set VLLM_CPP_MLX=off for a Metal build without it (smaller image, slower).
	ifeq ($(VLLM_CPP_MLX),on)
		MLX_ENABLED=1
	endif
else
	CMAKE_ARGS+=-DVLLM_CPP_CUDA=OFF
endif

UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
	LIB=libvllm.dylib
	# Apple Clang diagnoses a pair of constant-folded array bounds in the Metal
	# build as a GNU extension. Disable that diagnostic for both Objective-C and
	# C++ because vllm.cpp appends target-local -Werror after these global flags.
	CMAKE_ARGS+=-DCMAKE_CXX_FLAGS=-Wno-gnu-folding-constant
	CMAKE_ARGS+=-DCMAKE_OBJC_FLAGS=-Wno-gnu-folding-constant
	CMAKE_ARGS+=-DCMAKE_OBJCXX_FLAGS=-Wno-gnu-folding-constant
else
	LIB=libvllm.so
endif

# patches/ carries fixes the pinned engine SHA does not have yet. `git apply`
# is deliberately unguarded: a patch that no longer applies must FAIL the clone
# loudly, because the alternative is a pin that silently ships without a fix it
# is documented to carry. Each patch header says which pin retires it.
VLLM_CPP_PATCHES=$(wildcard patches/*.patch)

sources/vllm.cpp: $(VLLM_CPP_PATCHES)
	rm -rf sources/vllm.cpp
	mkdir -p sources/vllm.cpp
	cd sources/vllm.cpp && \
	git init && \
	git remote add origin $(VLLM_CPP_REPO) && \
	git fetch --depth 1 origin $(VLLM_CPP_VERSION) && \
	git checkout FETCH_HEAD && \
	for p in $(VLLM_CPP_PATCHES); do \
		echo "==> applying $$p"; \
		git apply ../../$$p || exit 1; \
	done

ifeq ($(MLX_ENABLED),1)
# A stamp FILE, not a phony target: a phony prerequisite is always "newer" than
# $(LIB) and would re-link libvllm on every invocation. Keyed on the version so
# a MLX_VERSION bump reinstalls instead of silently reusing the old wheel.
MLX_STAMP=$(MLX_VENV)/.mlx-$(MLX_VERSION).stamp
MLX_CMAKE_ARGS=-DVLLM_CPP_MLX=ON -DMLX_ROOT=$(MLX_ROOT)

$(MLX_STAMP):
	@if [ ! -x "$(MLX_VENV)/bin/pip" ]; then \
		python3 -m venv "$(MLX_VENV)" || { echo "vllm-cpp: python3 with venv is required to build the MLX provider; pass VLLM_CPP_MLX=off to build Metal without it" >&2; exit 1; }; \
	fi
	"$(MLX_VENV)"/bin/pip install --quiet --disable-pip-version-check "mlx==$(MLX_VERSION)"
	@# Resolved in the SHELL, not by $(MLX_ROOT): make expands a whole recipe
	@# before running its first line, so the glob would still be unmatched here.
	@# Every later use (the cmake args, package.sh) expands after this target has
	@# completed, where $(MLX_ROOT) does resolve.
	@root=$$(echo "$(MLX_VENV)"/lib/python*/site-packages/mlx); \
	test -f "$$root/lib/libmlx.dylib" -a -f "$$root/include/mlx/array.h" || \
		{ echo "vllm-cpp: mlx==$(MLX_VERSION) did not provide lib/libmlx.dylib + include/mlx/array.h under $$root" >&2; exit 1; }
	touch $@
else
MLX_STAMP=
MLX_CMAKE_ARGS=
endif

# govllmcpp.go mirrors vllm.h by hand, and the only guard against the two
# drifting apart is the vllm_abi_version check inside registerLib - which fires
# at runtime, on the user's machine, taking down every model load (issue
# #11379). Compare the two here instead, so moving VLLM_CPP_VERSION past the
# mirrors turns the build red while the header is still around to diff.
abi-check: sources/vllm.cpp
	@engine=$$(sed -n 's/^#define VLLM_ABI_VERSION \([0-9][0-9]*\).*/\1/p' sources/vllm.cpp/include/vllm.h); \
	backend=$$(sed -n 's/^const abiVersion = \([0-9][0-9]*\).*/\1/p' govllmcpp.go); \
	if [ -z "$$engine" ] || [ -z "$$backend" ]; then \
		echo "vllm-cpp: cannot read the ABI version (engine='$$engine' backend='$$backend')" >&2; exit 1; \
	fi; \
	if [ "$$engine" != "$$backend" ]; then \
		echo "vllm-cpp: ABI mismatch: vllm.cpp $(VLLM_CPP_VERSION) is v$$engine, govllmcpp.go mirrors v$$backend." >&2; \
		echo "  Update the struct mirrors and abiVersion in govllmcpp.go (and the offsets in vllmcpp_test.go) to v$$engine." >&2; \
		exit 1; \
	fi; \
	echo "vllm-cpp: ABI v$$engine matches the pinned engine"

$(LIB): sources/vllm.cpp $(MLX_STAMP)
	$(MAKE) abi-check
	mkdir -p build && \
	cd build && \
	cmake ../sources/vllm.cpp $(CMAKE_ARGS) $(MLX_CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) --target vllm_shared
	cp -fL build/$(LIB) ./$(LIB)

vllm-cpp: main.go govllmcpp.go backend.go chat.go options.go video.go $(LIB)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o vllm-cpp ./

package: vllm-cpp
	MLX_ROOT="$(MLX_ROOT)" bash package.sh

build: package

clean: purge
	rm -rf libvllm.so libvllm.dylib package sources/vllm.cpp vllm-cpp "$(MLX_VENV)"

purge:
	rm -rf build

.PHONY: abi-check

.NOTPARALLEL:

# The unit specs are pure Go (struct mirrors, option mapping, load
# validation): no libvllm build is needed. The e2e specs skip unless
# VLLM_CPP_MODEL points at a real model (then build the lib first).
test:
	@echo "Running vllm-cpp tests..."
	bash test.sh
	@echo "vllm-cpp tests completed."

all: vllm-cpp package
