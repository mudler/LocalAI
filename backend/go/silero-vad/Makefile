
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

# Detect if we are running on arm64
ifneq (,$(findstring aarch64,$(shell uname -m)))
	ONNX_ARCH=aarch64
endif

ifeq ($(OS),Darwin)
	ONNX_OS=osx
	ONNX_FLAVOR=
	ifneq (,$(findstring aarch64,$(shell uname -m)))
		ONNX_ARCH=arm64
	else ifneq (,$(findstring arm64,$(shell uname -m)))
		ONNX_ARCH=arm64
	else
		ONNX_ARCH=x86_64
	endif
endif

ONNX_ASSET=onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)$(if $(ONNX_VARIANT),$(ONNX_VARIANT)-$(ONNX_VERSION)-patched,$(ONNX_FLAVOR)-$(ONNX_VERSION))

sources/onnxruntime:
	mkdir -p sources/onnxruntime
	curl -fL $(ONNX_BASE_URL)/$(ONNX_ASSET).zip -o sources/onnxruntime/onnxruntime.zip
	cd sources/onnxruntime && unzip -q onnxruntime.zip && rm onnxruntime.zip
	cd sources/onnxruntime && mv onnxruntime-*/* ./ && rmdir onnxruntime-*

# The mirror's libraries are unversioned (soname libonnxruntime.so), so no
# rename is needed.
backend-assets/lib/libonnxruntime: sources/onnxruntime
	mkdir -p backend-assets/lib
	cp -rfLv sources/onnxruntime/lib/* backend-assets/lib/

silero-vad: backend-assets/lib/libonnxruntime
	CGO_LDFLAGS="$(CGO_LDFLAGS)" CPATH="$(CPATH):$(CURRENT_DIR)/sources/onnxruntime/include/" LIBRARY_PATH=$(CURRENT_DIR)/backend-assets/lib \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o silero-vad ./

package:
	bash package.sh

build: silero-vad package

clean:
	rm -f silero-vad