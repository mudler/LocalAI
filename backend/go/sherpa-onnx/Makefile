CURRENT_DIR=$(abspath ./)
GOCMD=go

ONNX_VERSION?=1.23.2
SHERPA_COMMIT?=7e227a529be6c383134a358c5744d0eb1cb5ae1f
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

ifeq ($(BUILD_TYPE),cublas)
	ONNX_VARIANT=-gpu
	SHERPA_GPU=ON
	ONNX_PROVIDER=cuda
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
	cd sources/sherpa-onnx/build && cmake \
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
	  -DONNXRUNTIME_DIR=$(CURRENT_DIR)/sources/onnxruntime \
	  ..
	cd sources/sherpa-onnx/build && make -j$(JOBS)

backend-assets/lib: sources/sherpa-onnx sources/onnxruntime
	mkdir -p backend-assets/lib
	cp -rfLv sources/onnxruntime/lib/* backend-assets/lib/
	cp -rfLv sources/sherpa-onnx/build/lib/*.so* backend-assets/lib/ 2>/dev/null || true
	cp -rfLv sources/sherpa-onnx/build/lib/*.dylib backend-assets/lib/ 2>/dev/null || true

sherpa-onnx: backend-assets/lib
	CGO_LDFLAGS="$(CGO_LDFLAGS) -L$(CURRENT_DIR)/backend-assets/lib -lsherpa-onnx-c-api -lonnxruntime" \
	CGO_CFLAGS="$(CGO_CFLAGS) -I$(CURRENT_DIR)/sources/sherpa-onnx/sherpa-onnx/c-api -I$(CURRENT_DIR)/sources/onnxruntime/include" \
	LIBRARY_PATH=$(CURRENT_DIR)/backend-assets/lib \
	$(GOCMD) build -ldflags "$(LD_FLAGS) -X main.onnxProvider=$(ONNX_PROVIDER)" -tags "$(GO_TAGS)" -o sherpa-onnx ./

package:
	bash package.sh

build: sherpa-onnx package

clean:
	rm -rf sherpa-onnx sources/ backend-assets/ package/ vits-ljs/ sherpa-onnx-whisper-*/

test: sherpa-onnx
	CGO_LDFLAGS="$(CGO_LDFLAGS) -L$(CURRENT_DIR)/backend-assets/lib -lsherpa-onnx-c-api -lonnxruntime" \
	CGO_CFLAGS="$(CGO_CFLAGS) -I$(CURRENT_DIR)/sources/sherpa-onnx/sherpa-onnx/c-api -I$(CURRENT_DIR)/sources/onnxruntime/include" \
	LIBRARY_PATH=$(CURRENT_DIR)/backend-assets/lib \
	LD_LIBRARY_PATH=$(CURRENT_DIR)/backend-assets/lib \
	bash test.sh

.PHONY: build package clean test
