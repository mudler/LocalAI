
CURRENT_DIR=$(abspath ./)
GOCMD=go

ONNX_VERSION?=1.20.0
ONNX_ARCH?=x64
ONNX_OS?=linux

# Detect if we are running on arm64
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

sources/onnxruntime:
	mkdir -p sources/onnxruntime
	curl -L https://github.com/microsoft/onnxruntime/releases/download/v$(ONNX_VERSION)/onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(ONNX_VERSION).tgz -o sources/onnxruntime/onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(ONNX_VERSION).tgz
	cd sources/onnxruntime && tar -xvf onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(ONNX_VERSION).tgz && rm onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(ONNX_VERSION).tgz
	cd sources/onnxruntime && mv onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(ONNX_VERSION)/* ./

backend-assets/lib/libonnxruntime.so.1: sources/onnxruntime
	mkdir -p backend-assets/lib
	cp -rfLv sources/onnxruntime/lib/* backend-assets/lib/
ifeq ($(OS),Darwin)
	mv backend-assets/lib/libonnxruntime.$(ONNX_VERSION).dylib backend-assets/lib/libonnxruntime.dylib
else
	mv backend-assets/lib/libonnxruntime.so.$(ONNX_VERSION) backend-assets/lib/libonnxruntime.so.1
endif

silero-vad: backend-assets/lib/libonnxruntime.so.1
	CGO_LDFLAGS="$(CGO_LDFLAGS)" CPATH="$(CPATH):$(CURRENT_DIR)/sources/onnxruntime/include/" LIBRARY_PATH=$(CURRENT_DIR)/backend-assets/lib \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o silero-vad ./

package:
	bash package.sh

build: silero-vad package