# dllm backend Makefile.
#
# Upstream pin lives below as DLLM_VERSION?=<sha> so .github/bump_deps.sh
# can find and update it - matches the whisper.cpp / parakeet-cpp / ds4
# convention.
#
# Local dev shortcut: if you already have an out-of-tree dllm.cpp build,
# you can symlink the .so into this directory and skip the clone/cmake
# steps entirely, e.g.:
#
#   ln -sf /path/to/dllm.cpp/build/libdllm.so .
#   go build -o dllm-grpc .
#
# That's what the gated C-ABI binding smoke uses (DLLM_TEST_LIBRARY). The
# default target below does the proper clone-at-pin + cmake build so CI
# doesn't need a side-checkout.
#
# NOTE: github.com/mudler/dllm.cpp is still private (publishing is planned);
# until then the anonymous clone below fails. Use the symlink shortcut above
# with a local checkout, or a git credential helper with access to the repo.

# The pin below is the P5 performance-parity head (device-resident
# self-conditioning, full-GPU placement at ngl >= n_layer, graph reuse,
# device-side EB reductions: ~8x per-step on GB10, see dllm.cpp
# docs/validation.md section 10). C-ABI unchanged (still version 1). It
# also carries the multimodal entry points (dllm_capi_generate_mm /
# dllm_capi_generate_stream_mm) the image-input path probes for; older
# libs still load, but image requests then fail with "library predates
# the multimodal entry points".
DLLM_VERSION?=320b57756efc3460169b8ea9e8c782867198f2a5
DLLM_REPO?=https://github.com/mudler/dllm.cpp

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

BUILD_TYPE?=
NATIVE?=false

# libdllm.so is self-contained: dllm.cpp's CMakeLists statically absorbs ggml
# (BUILD_SHARED_LIBS=OFF + PIC) into the shared lib, so dlopen needs no
# libggml*.so alongside it, only system libs (libstdc++/libgomp/libc) the
# runtime image already provides. Tests/CLI are upstream-only concerns.
CMAKE_ARGS?=-DCMAKE_BUILD_TYPE=Release -DDLLM_BUILD_TESTS=OFF

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# Same arch set the sibling ggml backends (acestep/vibevoice/qwen3-tts) bake
# for their cublas images; override for a native build.
CUDA_ARCHITECTURES?=75-virtual;80-virtual;86-real;89-real

# dllm.cpp gates CUDA behind DLLM_CUDA (set(GGML_CUDA ... CACHE FORCE)), so
# forward that instead of a bare -DGGML_CUDA=ON.
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DDLLM_CUDA=ON -DCMAKE_CUDA_ARCHITECTURES="$(CUDA_ARCHITECTURES)"
endif

.PHONY: dllm-grpc package build clean purge test all

all: dllm-grpc

# Clone the upstream dllm.cpp source at the pinned commit (ggml comes in as
# a submodule). Directory acts as the target so make only re-clones when
# missing. After a DLLM_VERSION bump, run 'make purge && make' to refetch.
sources/dllm.cpp:
	mkdir -p sources/dllm.cpp
	cd sources/dllm.cpp && \
	git init -q && \
	git remote add origin $(DLLM_REPO) && \
	git fetch --depth 1 origin $(DLLM_VERSION) && \
	git checkout FETCH_HEAD && \
	git submodule update --init --recursive --depth 1 --single-branch

# Build the shared lib out-of-tree, then stage it next to the Go sources so
# purego.Dlopen("libdllm.so") and the packaging step both pick it up.
libdllm.so: sources/dllm.cpp
	cmake -B sources/dllm.cpp/build -S sources/dllm.cpp $(CMAKE_ARGS)
	cmake --build sources/dllm.cpp/build --config Release -j$(JOBS)
	cp -fv sources/dllm.cpp/build/libdllm.so ./

dllm-grpc: libdllm.so main.go capi.go
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o dllm-grpc .

package: dllm-grpc
	bash package.sh

build: package

# Test target. The C-ABI binding smoke is gated on DLLM_TEST_LIBRARY +
# DLLM_TEST_TINY_MODEL; without them the gated specs auto-skip and only the
# pure-Go helper specs run.
test:
	LD_LIBRARY_PATH=$(CURDIR):$$LD_LIBRARY_PATH $(GOCMD) test ./... -count=1

clean: purge
	rm -rf libdllm.so* package dllm-grpc

purge:
	rm -rf sources/dllm.cpp
