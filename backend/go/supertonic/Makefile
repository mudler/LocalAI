CURRENT_DIR=$(abspath ./)
GOCMD=go

ONNX_VERSION?=1.24.4
ONNX_ARCH?=x64
ONNX_OS?=linux

ifneq (,$(findstring aarch64,$(shell uname -m)))
	ONNX_ARCH=aarch64
endif

ifeq ($(OS),Darwin)
	ONNX_OS=osx
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
		ONNX_VARIANT=-gpu
	endif
else
	ONNX_VARIANT=
	ONNX_PROVIDER=cpu
endif

sources/onnxruntime:
	mkdir -p sources/onnxruntime
	curl -L https://github.com/microsoft/onnxruntime/releases/download/v$(ONNX_VERSION)/onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)$(ONNX_VARIANT)-$(ONNX_VERSION).tgz \
	  -o sources/onnxruntime/onnxruntime.tgz
	cd sources/onnxruntime && tar -xf onnxruntime.tgz --strip-components=1 && rm onnxruntime.tgz

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
