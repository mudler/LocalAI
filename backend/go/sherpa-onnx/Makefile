CURRENT_DIR=$(abspath ./)
GOCMD=go

ONNX_VERSION?=1.24.4
# v1.12.39 — includes upstream's onnxruntime 1.24.4 bump (#3501). Earlier
# pinned commits only support onnxruntime 1.23.2, which has no CUDA 13
# pre-built tarball, blocking the -gpu-nvidia-cuda-13 build matrix entry.
SHERPA_COMMIT?=7288d15e3e31a7bd589b2ba88828d521e7a6b140
ONNX_ARCH?=x64
ONNX_OS?=linux

ifneq (,$(findstring aarch64,$(shell uname -m)))
	ONNX_ARCH=aarch64
endif

ifeq ($(OS),Darwin)
	ONNX_OS=osx
	ifneq (,$(findstring aarch64,$(shell uname -m)))
		ONNX_ARCH=arm64
	else ifneq (,$(findstring arm64,$(shell uname -m)))
		ONNX_ARCH=arm64
	else
		ONNX_ARCH=x86_64
	endif
endif

# Upstream onnxruntime ships CUDA 12 and CUDA 13 variants under different
# names: -gpu-<ver>.tgz for CUDA 12, -gpu_cuda13-<ver>.tgz for CUDA 13
# (note underscore vs dash). CUDA 13 tarballs only exist from 1.24.x onward.
ifeq ($(BUILD_TYPE),cublas)
	SHERPA_GPU=ON
	ONNX_PROVIDER=cuda
	ifeq ($(CUDA_MAJOR_VERSION),13)
		ONNX_VARIANT=-gpu_cuda13
	else
		ONNX_VARIANT=-gpu
	endif
else
	ONNX_VARIANT=
	SHERPA_GPU=OFF
	ONNX_PROVIDER=cpu
endif

JOBS?=$(shell nproc --ignore=1 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

sources/onnxruntime:
	mkdir -p sources/onnxruntime
	curl -L https://github.com/microsoft/onnxruntime/releases/download/v$(ONNX_VERSION)/onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)$(ONNX_VARIANT)-$(ONNX_VERSION).tgz \
	  -o sources/onnxruntime/onnxruntime.tgz
	cd sources/onnxruntime && tar -xf onnxruntime.tgz --strip-components=1 && rm onnxruntime.tgz

sources/sherpa-onnx: sources/onnxruntime
	git clone https://github.com/k2-fsa/sherpa-onnx.git sources/sherpa-onnx
	cd sources/sherpa-onnx && git checkout $(SHERPA_COMMIT)
	mkdir -p sources/sherpa-onnx/build
	# sherpa-onnx's cmake detects a pre-installed onnxruntime via the
	# SHERPA_ONNXRUNTIME_{INCLUDE,LIB}_DIR env vars (not via -D flags).
	# Point them at our locally-downloaded Microsoft tarball — without
	# this, sherpa-onnx falls through to download_onnxruntime() which
	# fetches from csukuangfj/onnxruntime-libs. For the GPU 1.24.4
	# build that release mirror publishes `-patched.zip` instead of the
	# expected `.tgz`, so the download 404s and the build fails.
	cd sources/sherpa-onnx/build && \
	SHERPA_ONNXRUNTIME_INCLUDE_DIR=$(CURRENT_DIR)/sources/onnxruntime/include \
	SHERPA_ONNXRUNTIME_LIB_DIR=$(CURRENT_DIR)/sources/onnxruntime/lib \
	cmake \
	  -DCMAKE_BUILD_TYPE=Release \
	  -DCMAKE_C_FLAGS="-Wno-error=format-security" \
	  -DCMAKE_CXX_FLAGS="-Wno-error=format-security" \
	  -DSHERPA_ONNX_ENABLE_GPU=$(SHERPA_GPU) \
	  -DSHERPA_ONNX_ENABLE_TTS=ON \
	  -DSHERPA_ONNX_ENABLE_BINARY=OFF \
	  -DSHERPA_ONNX_ENABLE_PYTHON=OFF \
	  -DSHERPA_ONNX_ENABLE_TESTS=OFF \
	  -DSHERPA_ONNX_ENABLE_C_API=ON \
	  -DBUILD_SHARED_LIBS=ON \
	  -DSHERPA_ONNX_USE_PRE_INSTALLED_ONNXRUNTIME_IF_AVAILABLE=ON \
	  ..
	cd sources/sherpa-onnx/build && make -j$(JOBS)

backend-assets/lib: sources/sherpa-onnx sources/onnxruntime
	mkdir -p backend-assets/lib
	cp -rfLv sources/onnxruntime/lib/* backend-assets/lib/
	cp -rfLv sources/sherpa-onnx/build/lib/*.so* backend-assets/lib/ 2>/dev/null || true
	cp -rfLv sources/sherpa-onnx/build/lib/*.dylib backend-assets/lib/ 2>/dev/null || true

# libsherpa-shim wraps sherpa-onnx's nested config structs and TTS
# callback plumbing behind a purego-friendly API: opaque handles plus
# fixed-signature setters/getters/trampoline. Plain C compile — no cgo.
SHIM_EXT=so
ifeq ($(OS),Darwin)
	SHIM_EXT=dylib
endif

backend-assets/lib/libsherpa-shim.$(SHIM_EXT): csrc/shim.c csrc/shim.h backend-assets/lib
	$(CC) -shared -fPIC -O2 \
	  -I$(CURRENT_DIR)/sources/sherpa-onnx/sherpa-onnx/c-api \
	  -o $@ csrc/shim.c \
	  -L$(CURRENT_DIR)/backend-assets/lib \
	  -lsherpa-onnx-c-api \
	  -Wl,-rpath,'$$ORIGIN'

sherpa-onnx: backend-assets/lib backend-assets/lib/libsherpa-shim.$(SHIM_EXT)
	CGO_ENABLED=0 $(GOCMD) build \
	  -ldflags "$(LD_FLAGS) -X main.onnxProvider=$(ONNX_PROVIDER)" \
	  -tags "$(GO_TAGS)" -o sherpa-onnx ./

package:
	bash package.sh

build: sherpa-onnx package

clean:
	rm -rf sherpa-onnx sources/ backend-assets/ package/ vits-ljs/ sherpa-onnx-whisper-*/

test: sherpa-onnx
	LD_LIBRARY_PATH=$(CURRENT_DIR)/backend-assets/lib \
	bash test.sh

.PHONY: build package clean test
