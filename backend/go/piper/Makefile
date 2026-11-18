
# go-piper version
PIPER_REPO?=https://github.com/mudler/go-piper
PIPER_VERSION?=e10ca041a885d4a8f3871d52924b47792d5e5aa0

CURRENT_DIR=$(abspath ./)
GOCMD=go

# piper-phonemize downloads onnxruntime 1.14.1 from microsoft/onnxruntime
# (404 now) unless the directory it expects already exists. Put the
# prebuilt runtime, hosted at github.com/mudler/onnxruntime-libs, there
# before the build. The 1.28.2 build keeps the C++ API piper uses.
ONNX_VERSION?=1.28.2
ONNX_BASE_URL?=https://github.com/mudler/onnxruntime-libs/releases/download/v$(ONNX_VERSION)
ONNX_FLAVOR?=-glibc2_17-Release
ONNX_ARCH?=x64
ONNX_OS?=linux
# The name of the directory that piper-phonemize looks for.
PHONEMIZE_ONNX_VERSION?=1.14.1

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

ONNX_ASSET=onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)$(ONNX_FLAVOR)-$(ONNX_VERSION)

PIPER_CGO_CXXFLAGS+=-I$(CURRENT_DIR)/sources/go-piper/piper/src/cpp -I$(CURRENT_DIR)/sources/go-piper/piper/build/fi/include -I$(CURRENT_DIR)/sources/go-piper/piper/build/pi/include -I$(CURRENT_DIR)/sources/go-piper/piper/build/si/include
PIPER_CGO_LDFLAGS+=-L$(CURRENT_DIR)/sources/go-piper/piper/build/fi/lib -L$(CURRENT_DIR)/sources/go-piper/piper/build/pi/lib -L$(CURRENT_DIR)/sources/go-piper/piper/build/si/lib -lfmt -lspdlog -lucd

## go-piper
sources/go-piper:
	mkdir -p sources/go-piper
	cd sources/go-piper && \
	git init && \
	git remote add origin $(PIPER_REPO) && \
	git fetch origin && \
	git checkout $(PIPER_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch
	mkdir -p sources/go-piper/piper-phonemize/lib/onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(PHONEMIZE_ONNX_VERSION)
	curl -fL $(ONNX_BASE_URL)/$(ONNX_ASSET).zip -o sources/go-piper/onnxruntime.zip
	cd sources/go-piper && unzip -q onnxruntime.zip -d onnxruntime-tmp && rm onnxruntime.zip
	cp -a sources/go-piper/onnxruntime-tmp/onnxruntime-*/. sources/go-piper/piper-phonemize/lib/onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(PHONEMIZE_ONNX_VERSION)/
	rm -rf sources/go-piper/onnxruntime-tmp

sources/go-piper/libpiper_binding.a: sources/go-piper
	$(MAKE) -C sources/go-piper libpiper_binding.a example/main piper.o

espeak-ng-data: sources/go-piper sources/go-piper/libpiper_binding.a
	mkdir -p espeak-ng-data
	@cp -rf sources/go-piper/piper-phonemize/pi/share/espeak-ng-data/. espeak-ng-data

piper: sources/go-piper sources/go-piper/libpiper_binding.a espeak-ng-data
	$(GOCMD) mod edit -replace github.com/mudler/go-piper=$(CURRENT_DIR)/sources/go-piper
	CGO_CXXFLAGS="$(PIPER_CGO_CXXFLAGS)" CGO_LDFLAGS="$(PIPER_CGO_LDFLAGS)" LIBRARY_PATH=$(CURRENT_DIR)/sources/go-piper \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o piper ./

package:
	bash package.sh

build: piper package

clean:
	rm -f piper