CURRENT_DIR=$(abspath ./)
GOCMD=go

ONNX_VERSION?=1.28.2
# Microsoft's onnxruntime release page is gone (404), so the prebuilt
# runtimes come from the mirror that sherpa-onnx itself builds against.
# Same builds, but zip files, and the libraries are not versioned.
# We host unmodified copies of the mirror's assets, with the same file names,
# in https://github.com/mudler/onnxruntime-libs (SHA256 values in its README).
ONNX_BASE_URL?=https://github.com/mudler/onnxruntime-libs/releases/download/v$(ONNX_VERSION)
ONNX_FLAVOR?=-glibc2_17-Release
ONNX_ARCH?=x64
ONNX_OS?=linux

ifneq (,$(findstring aarch64,$(shell uname -m)))
	ONNX_ARCH=aarch64
endif

ifeq ($(OS),Darwin)
	ONNX_OS=osx
	ONNX_FLAVOR=
	ifneq (,$(findstring arm64,$(shell uname -m)))
		ONNX_ARCH=arm64
	else
		ONNX_ARCH=x86_64
	endif
endif

# CUDA 12 ships as -gpu, CUDA 13 as -gpu_cuda13 (underscore). CPU has no suffix.
ifeq ($(BUILD_TYPE),cublas)
	ONNX_PROVIDER=cuda
	ifeq ($(CUDA_MAJOR_VERSION),13)
		ONNX_VARIANT=-gpu_cuda13
	else
		ONNX_VARIANT=-gpu_cuda12
	endif
else
	ONNX_VARIANT=
	ONNX_PROVIDER=cpu
endif

ONNX_ASSET=onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)$(if $(ONNX_VARIANT),$(ONNX_VARIANT)-$(ONNX_VERSION)-patched,$(ONNX_FLAVOR)-$(ONNX_VERSION))

sources/onnxruntime:
	mkdir -p sources/onnxruntime
	curl -fL $(ONNX_BASE_URL)/$(ONNX_ASSET).zip -o sources/onnxruntime/onnxruntime.zip
	cd sources/onnxruntime && unzip -q onnxruntime.zip && rm onnxruntime.zip
	cd sources/onnxruntime && mv onnxruntime-*/* ./ && rmdir onnxruntime-*

backend-assets/lib: sources/onnxruntime
	mkdir -p backend-assets/lib
	cp -rfLv sources/onnxruntime/lib/* backend-assets/lib/

supertonic: backend-assets/lib
	CGO_ENABLED=1 $(GOCMD) build \
	  -ldflags "$(LD_FLAGS) -X main.onnxProvider=$(ONNX_PROVIDER)" \
	  -tags "$(GO_TAGS)" -o supertonic ./

package:
	bash package.sh

build: supertonic package

# Tests need only the Go toolchain (gcc); yalue dlopens onnxruntime at
# runtime, so no tarball download is required to compile or run unit specs.
test:
	CGO_ENABLED=1 $(GOCMD) test -v -timeout 120s ./...

clean:
	rm -rf supertonic sources/ backend-assets/ package/

.PHONY: build package clean test
