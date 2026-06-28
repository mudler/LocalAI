# voice-detect backend Makefile.
#
# Upstream pin lives below as VOICEDETECT_VERSION?=3d510772357538c5182808ac7de2278b84824e24
# can find and update it - matches the parakeet.cpp / whisper.cpp / ds4 convention).
#
# Local dev shortcut: if you already have an out-of-tree voice-detect.cpp build,
# symlink the .so + header into this directory and skip the clone/cmake steps:
#
#   ln -sf /path/to/voice-detect.cpp/build-shared/libvoicedetect.so .
#   ln -sf /path/to/voice-detect.cpp/include/voicedetect_capi.h .
#   go build -o voice-detect-grpc .
#
# The default target below does the proper clone-at-pin + cmake build so CI does
# not need a side-checkout.

VOICEDETECT_VERSION?=3d510772357538c5182808ac7de2278b84824e24
VOICEDETECT_REPO?=https://github.com/mudler/voice-detect.cpp

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

BUILD_TYPE?=
NATIVE?=false

# Resolve the target arch. The backend matrix / Docker build pass TARGETARCH
# (amd64|arm64); fall back to uname -m (aarch64|x86_64) for a local build.
RECON_ARCH?=$(or $(TARGETARCH),$(shell uname -m))

# Build ggml statically into libvoicedetect.so (PIC) so the shared lib is
# self-contained: dlopen needs no libggml*.so alongside it, only system libs
# (libstdc++/libgomp/libc) that the runtime image already provides.
CMAKE_ARGS?=-DCMAKE_BUILD_TYPE=Release -DVOICEDETECT_SHARED=ON -DVOICEDETECT_BUILD_CLI=OFF -DVOICEDETECT_BUILD_TESTS=OFF -DBUILD_SHARED_LIBS=OFF -DCMAKE_POSITION_INDEPENDENT_CODE=ON

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# voice-detect.cpp gates its GGML backends behind VOICEDETECT_GGML_* options and
# does set(GGML_CUDA ${VOICEDETECT_GGML_CUDA} CACHE BOOL "" FORCE), so a bare
# -DGGML_CUDA=ON is overwritten back to OFF. Forward the VOICEDETECT_GGML_*
# options instead. (openblas is not gated, so -DGGML_BLAS passes through.)
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DVOICEDETECT_GGML_CUDA=ON
	# Opt-in cuDNN implicit-GEMM conv path (kills im2col on GPU, reaches
	# torch-cuDNN parity). Only the arm64 + CUDA 13 image (GB10/Jetson/L4T)
	# ships libcudnn9 + the -dev headers, so gate cuDNN to that variant.
	# x86 CUDA images carry no cuDNN -> enabling it there is a link failure.
	ifeq ($(CUDA_MAJOR_VERSION),13)
	ifneq (,$(filter arm64 aarch64,$(RECON_ARCH)))
		CMAKE_ARGS+=-DVOICEDETECT_GGML_CUDNN=ON
	endif
	endif
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DVOICEDETECT_GGML_HIP=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DVOICEDETECT_GGML_VULKAN=ON
else ifeq ($(BUILD_TYPE),metal)
	CMAKE_ARGS+=-DVOICEDETECT_GGML_METAL=ON
endif

.PHONY: voice-detect-grpc package build clean purge test all

all: voice-detect-grpc

# Clone the upstream voice-detect.cpp source at the pinned commit. Directory acts
# as the target so make only re-clones when missing. After a VOICEDETECT_VERSION
# bump, run 'make purge && make' to refetch.
sources/voice-detect.cpp:
	mkdir -p sources/voice-detect.cpp
	cd sources/voice-detect.cpp && \
	git init -q && \
	git remote add origin $(VOICEDETECT_REPO) && \
	git fetch --depth 1 origin $(VOICEDETECT_VERSION) && \
	git checkout FETCH_HEAD && \
	git submodule update --init --recursive --depth 1 --single-branch

# Build the shared lib + header out-of-tree, then stage them next to the Go
# sources so purego.Dlopen("libvoicedetect.so") and the cgo-less build both pick
# them up.
libvoicedetect.so: sources/voice-detect.cpp
	cmake -B sources/voice-detect.cpp/build-shared -S sources/voice-detect.cpp $(CMAKE_ARGS)
	cmake --build sources/voice-detect.cpp/build-shared --config Release -j$(JOBS) --target voicedetect
	cp -fv sources/voice-detect.cpp/build-shared/libvoicedetect.so* ./ 2>/dev/null || true
	cp -fv sources/voice-detect.cpp/include/voicedetect_capi.h ./

voice-detect-grpc: libvoicedetect.so main.go govoicedetect.go options.go
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o voice-detect-grpc .

package: voice-detect-grpc
	bash package.sh

build: package

# Test target. The embed/verify/analyze smoke specs are gated on
# VOICEDETECT_BACKEND_TEST_MODEL + VOICEDETECT_BACKEND_TEST_WAV; without them the
# heavy specs auto-skip and only the pure-Go parsing specs run.
test:
	LD_LIBRARY_PATH=$(CURDIR):$$LD_LIBRARY_PATH $(GOCMD) test ./... -count=1

clean: purge
	rm -rf libvoicedetect.so* voicedetect_capi.h package voice-detect-grpc

purge:
	rm -rf sources/voice-detect.cpp
