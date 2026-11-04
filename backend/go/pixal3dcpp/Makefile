.DEFAULT_GOAL := all
PIXAL3D_REPO?=https://github.com/raven38/pixal3d.cpp
PIXAL3D_VERSION?=d1b4926452f9e09702b891db5f05acc845e153ed
GOCMD?=go
GO_TAGS?=
BUILD_TYPE?=
JOBS?=2
CMAKE_ARGS?=
CMAKE_ARGS+=-DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=ON -DGGML_NATIVE=OFF -DTRELLIS_WEBP=OFF
# A portable CPU fallback avoids instructions unavailable on the destination.
CMAKE_ARGS+=-DGGML_AVX=OFF -DGGML_AVX2=OFF -DGGML_AVX512=OFF -DGGML_FMA=OFF -DGGML_F16C=OFF -DGGML_BMI2=OFF
ifeq ($(BUILD_TYPE),cublas)
CMAKE_ARGS+=-DGGML_CUDA=ON -DCMAKE_CUDA_ARCHITECTURES=all-major
else ifeq ($(BUILD_TYPE),vulkan)
CMAKE_ARGS+=-DGGML_VULKAN=ON
else ifeq ($(BUILD_TYPE),metal)
CMAKE_ARGS+=-DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON
else
CMAKE_ARGS+=-DGGML_METAL=OFF
endif

sources/pixal3d:
	mkdir -p sources/pixal3d
	cd sources/pixal3d && git init -q && git remote add origin $(PIXAL3D_REPO) && \
		git fetch --depth 1 origin $(PIXAL3D_VERSION) && git checkout FETCH_HEAD && \
		git submodule update --init --recursive --depth 1

native/trellis-cli: sources/pixal3d
	cmake -S sources/pixal3d -B native $(CMAKE_ARGS)
	cmake --build native --target trellis-cli --config Release -j$(JOBS)

pixal3dcpp: main.go pixal3d.go
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o pixal3dcpp ./

package: pixal3dcpp native/trellis-cli
	bash package.sh

all: package
build: package

test:
	$(GOCMD) test -v ./...

purge:
	rm -rf native sources
clean: purge
	rm -rf pixal3dcpp package
.PHONY: all build package test purge clean

ifeq ($(shell uname -s),Darwin)
# Avoid a runtime dependency on Homebrew libomp in portable backend packages.
CMAKE_ARGS+=-DGGML_OPENMP=OFF
endif
