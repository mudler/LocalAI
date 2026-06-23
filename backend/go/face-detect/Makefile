# face-detect backend Makefile.
#
# Upstream pin lives below as FACEDETECT_VERSION?=d80092b... (.github/bump_deps.sh
# can find and update it - matches the voice-detect / parakeet.cpp / whisper.cpp
# convention).
#
# Local dev shortcut: if you already have an out-of-tree face-detect.cpp build,
# symlink the .so + header into this directory and skip the clone/cmake steps:
#
#   ln -sf /path/to/face-detect.cpp/build-shared/libfacedetect.so .
#   ln -sf /path/to/face-detect.cpp/include/facedetect_capi.h .
#   go build -o face-detect-grpc .
#
# The default target below does the proper clone-at-pin + cmake build so CI does
# not need a side-checkout.

FACEDETECT_VERSION?=d80092b1137f739eac814cfe5e1b38b04b003097
FACEDETECT_REPO?=https://github.com/mudler/face-detect.cpp

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

BUILD_TYPE?=
NATIVE?=false

# Build ggml + the vendored libjpeg-turbo statically into libfacedetect.so (PIC)
# so the shared lib is self-contained: dlopen needs no libggml*.so alongside it,
# only system libs (libstdc++/libgomp/libc) the runtime image already provides.
# The vendored jpeg symbols are hidden via -Wl,--exclude-libs,ALL on the C++
# side, so only the facedetect_capi_* surface is exported.
CMAKE_ARGS?=-DCMAKE_BUILD_TYPE=Release -DFACEDETECT_SHARED=ON -DFACEDETECT_BUILD_CLI=OFF -DFACEDETECT_BUILD_TESTS=OFF -DBUILD_SHARED_LIBS=OFF -DCMAKE_POSITION_INDEPENDENT_CODE=ON

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# face-detect.cpp gates its GGML backends behind FACEDETECT_GGML_* options and
# does set(GGML_CUDA ${FACEDETECT_GGML_CUDA} CACHE BOOL "" FORCE), so a bare
# -DGGML_CUDA=ON is overwritten back to OFF. Forward the FACEDETECT_GGML_*
# options instead. (openblas is not gated, so -DGGML_BLAS passes through.)
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DFACEDETECT_GGML_CUDA=ON
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DFACEDETECT_GGML_HIP=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DFACEDETECT_GGML_VULKAN=ON
else ifeq ($(BUILD_TYPE),metal)
	CMAKE_ARGS+=-DFACEDETECT_GGML_METAL=ON
endif

.PHONY: face-detect-grpc package build clean purge test all

all: face-detect-grpc

# Clone the upstream face-detect.cpp source at the pinned commit. Directory acts
# as the target so make only re-clones when missing. After a FACEDETECT_VERSION
# bump, run 'make purge && make' to refetch.
sources/face-detect.cpp:
	mkdir -p sources/face-detect.cpp
	cd sources/face-detect.cpp && \
	git init -q && \
	git remote add origin $(FACEDETECT_REPO) && \
	git fetch --depth 1 origin $(FACEDETECT_VERSION) && \
	git checkout FETCH_HEAD && \
	git submodule update --init --recursive --depth 1 --single-branch

# Build the shared lib + header out-of-tree, then stage them next to the Go
# sources so purego.Dlopen("libfacedetect.so") and the cgo-less build both pick
# them up.
libfacedetect.so: sources/face-detect.cpp
	cmake -B sources/face-detect.cpp/build-shared -S sources/face-detect.cpp $(CMAKE_ARGS)
	cmake --build sources/face-detect.cpp/build-shared --config Release -j$(JOBS) --target facedetect
	cp -fv sources/face-detect.cpp/build-shared/libfacedetect.so* ./ 2>/dev/null || true
	cp -fv sources/face-detect.cpp/include/facedetect_capi.h ./

face-detect-grpc: libfacedetect.so main.go gofacedetect.go options.go
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o face-detect-grpc .

package: face-detect-grpc
	bash package.sh

build: package

# Test target. The embed/detect/verify/analyze smoke specs are gated on
# FACEDETECT_BACKEND_TEST_MODEL + FACEDETECT_BACKEND_TEST_IMAGE; without them the
# heavy specs auto-skip and only the pure-Go parsing specs run.
test:
	LD_LIBRARY_PATH=$(CURDIR):$$LD_LIBRARY_PATH $(GOCMD) test ./... -count=1

clean: purge
	rm -rf libfacedetect.so* facedetect_capi.h package face-detect-grpc

purge:
	rm -rf sources/face-detect.cpp
