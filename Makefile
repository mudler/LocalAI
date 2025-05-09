GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_NAME=local-ai

DETECT_LIBS?=true

# llama.cpp versions
CPPLLAMA_VERSION?=f05a6d71a0f3dbf0730b56a1abbad41c0f42e63d

# whisper.cpp version
WHISPER_REPO?=https://github.com/ggml-org/whisper.cpp
WHISPER_CPP_VERSION?=cb2bd11ee86c6d2a8c8c22ea3043682cbf127bcd

# go-piper version
PIPER_REPO?=https://github.com/mudler/go-piper
PIPER_VERSION?=e10ca041a885d4a8f3871d52924b47792d5e5aa0

# bark.cpp
BARKCPP_REPO?=https://github.com/PABannier/bark.cpp.git
BARKCPP_VERSION?=v1.0.0

# stablediffusion.cpp (ggml)
STABLEDIFFUSION_GGML_REPO?=https://github.com/richiejp/stable-diffusion.cpp
STABLEDIFFUSION_GGML_VERSION?=53e3b17eb3d0b5760ced06a1f98320b68b34aaae

# ONEAPI variables for SYCL
export ONEAPI_VARS?=/opt/intel/oneapi/setvars.sh

ONNX_VERSION?=1.20.0
ONNX_ARCH?=x64
ONNX_OS?=linux

export BUILD_TYPE?=
export STABLE_BUILD_TYPE?=$(BUILD_TYPE)
export CMAKE_ARGS?=-DBUILD_SHARED_LIBS=OFF
export BACKEND_LIBS?=
export WHISPER_DIR=$(abspath ./sources/whisper.cpp)
export WHISPER_INCLUDE_PATH=$(WHISPER_DIR)/include:$(WHISPER_DIR)/ggml/include
export WHISPER_LIBRARY_PATH=$(WHISPER_DIR)/build/src/:$(WHISPER_DIR)/build/ggml/src

CGO_LDFLAGS?=
CGO_LDFLAGS_WHISPER?=
CGO_LDFLAGS_WHISPER+=-lggml
CUDA_LIBPATH?=/usr/local/cuda/lib64/
GO_TAGS?=
BUILD_ID?=
NATIVE?=false

TEST_DIR=/tmp/test

TEST_FLAKES?=5

RANDOM := $(shell bash -c 'echo $$RANDOM')

VERSION?=$(shell git describe --always --tags || echo "dev" )
# go tool nm ./local-ai | grep Commit
LD_FLAGS?=-s -w
override LD_FLAGS += -X "github.com/mudler/LocalAI/internal.Version=$(VERSION)"
override LD_FLAGS += -X "github.com/mudler/LocalAI/internal.Commit=$(shell git rev-parse HEAD)"

OPTIONAL_TARGETS?=

export OS := $(shell uname -s)
ARCH := $(shell uname -m)
GREEN  := $(shell tput -Txterm setaf 2)
YELLOW := $(shell tput -Txterm setaf 3)
WHITE  := $(shell tput -Txterm setaf 7)
CYAN   := $(shell tput -Txterm setaf 6)
RESET  := $(shell tput -Txterm sgr0)

UPX?=
# check if upx exists
ifeq (, $(shell which upx))
	UPX=
else
	UPX=$(shell which upx)
endif

# Default Docker bridge IP
E2E_BRIDGE_IP?=172.17.0.1

ifndef UNAME_S
UNAME_S := $(shell uname -s)
endif

# IF native is false, we add -DGGML_NATIVE=OFF to CMAKE_ARGS
ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

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

	ifeq ($(OSX_SIGNING_IDENTITY),)
		OSX_SIGNING_IDENTITY := $(shell security find-identity -v -p codesigning | grep '"' | head -n 1 | sed -E 's/.*"(.*)"/\1/')
	endif

	# on OSX, if BUILD_TYPE is blank, we should default to use Metal
	ifeq ($(BUILD_TYPE),)
		BUILD_TYPE=metal
	# disable metal if on Darwin and any other value is explicitly passed.
	else ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
		export GGML_NO_ACCELERATE=1
		export GGML_NO_METAL=1
	endif

	ifeq ($(BUILD_TYPE),metal)
#			-lcblas 	removed: it seems to always be listed as a duplicate flag.
		CGO_LDFLAGS += -framework Accelerate
		CGO_LDFLAGS_WHISPER+=-lggml-metal -lggml-blas
		CMAKE_ARGS+=-DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_USE_BF16=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
		CMAKE_ARGS+=-DWHISPER_BUILD_EXAMPLES=OFF
		CMAKE_ARGS+=-DWHISPER_BUILD_TESTS=OFF
		CMAKE_ARGS+=-DWHISPER_BUILD_SERVER=OFF
		CMAKE_ARGS+=-DGGML_OPENMP=OFF
		export WHISPER_LIBRARY_PATH:=$(WHISPER_LIBRARY_PATH):$(WHISPER_DIR)/build/ggml/src/ggml-metal/:$(WHISPER_DIR)/build/ggml/src/ggml-blas
	endif
else
CGO_LDFLAGS_WHISPER+=-lgomp
endif

ifeq ($(BUILD_TYPE),openblas)
	CGO_LDFLAGS+=-lopenblas
	export GGML_OPENBLAS=1
endif

ifeq ($(BUILD_TYPE),cublas)
	CGO_LDFLAGS+=-lcublas -lcudart -L$(CUDA_LIBPATH) -L$(CUDA_LIBPATH)/stubs/ -lcuda
	export GGML_CUDA=1
	CMAKE_ARGS+=-DGGML_CUDA=ON
	CGO_LDFLAGS_WHISPER+=-lcufft -lggml-cuda
	export WHISPER_LIBRARY_PATH:=$(WHISPER_LIBRARY_PATH):$(WHISPER_DIR)/build/ggml/src/ggml-cuda/
endif

ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=1
endif

ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	export GGML_SYCL=1
	CMAKE_ARGS+=-DGGML_SYCL=ON
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	export GGML_SYCL_F16=1
	CMAKE_ARGS+=-DGGML_SYCL_F16=ON
endif

ifeq ($(BUILD_TYPE),hipblas)
	ROCM_HOME ?= /opt/rocm
	ROCM_PATH ?= /opt/rocm
	LD_LIBRARY_PATH ?= /opt/rocm/lib:/opt/rocm/llvm/lib
	export CXX=$(ROCM_HOME)/llvm/bin/clang++
	export CC=$(ROCM_HOME)/llvm/bin/clang
	export STABLE_BUILD_TYPE=
	export GGML_HIP=1
	GPU_TARGETS ?= gfx803,gfx900,gfx906,gfx908,gfx90a,gfx942,gfx1010,gfx1030,gfx1032,gfx1100,gfx1101,gfx1102
	AMDGPU_TARGETS ?= "$(GPU_TARGETS)"
	CMAKE_ARGS+=-DGGML_HIP=ON -DAMDGPU_TARGETS="$(AMDGPU_TARGETS)" -DGPU_TARGETS="$(GPU_TARGETS)"
	CGO_LDFLAGS += -O3 --rtlib=compiler-rt -unwindlib=libgcc -lhipblas -lrocblas --hip-link -L${ROCM_HOME}/lib/llvm/lib
endif

ifeq ($(BUILD_TYPE),metal)
	CGO_LDFLAGS+=-framework Foundation -framework Metal -framework MetalKit -framework MetalPerformanceShaders
	export GGML_METAL=1
endif

ifeq ($(BUILD_TYPE),clblas)
	CGO_LDFLAGS+=-lOpenCL -lclblast
	export GGML_OPENBLAS=1
endif

# glibc-static or glibc-devel-static required
ifeq ($(STATIC),true)
	LD_FLAGS+=-linkmode external -extldflags -static
endif

ifeq ($(findstring tts,$(GO_TAGS)),tts)
#	OPTIONAL_TARGETS+=go-piper/libpiper_binding.a
#	OPTIONAL_TARGETS+=backend-assets/espeak-ng-data
	PIPER_CGO_CXXFLAGS+=-I$(CURDIR)/sources/go-piper/piper/src/cpp -I$(CURDIR)/sources/go-piper/piper/build/fi/include -I$(CURDIR)/sources/go-piper/piper/build/pi/include -I$(CURDIR)/sources/go-piper/piper/build/si/include
	PIPER_CGO_LDFLAGS+=-L$(CURDIR)/sources/go-piper/piper/build/fi/lib -L$(CURDIR)/sources/go-piper/piper/build/pi/lib -L$(CURDIR)/sources/go-piper/piper/build/si/lib -lfmt -lspdlog -lucd
	OPTIONAL_GRPC+=backend-assets/grpc/piper
endif

ALL_GRPC_BACKENDS=backend-assets/grpc/huggingface
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-cpp-avx
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-cpp-avx2
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-cpp-avx512
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-cpp-fallback
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-cpp-grpc
ALL_GRPC_BACKENDS+=backend-assets/util/llama-cpp-rpc-server
ALL_GRPC_BACKENDS+=backend-assets/grpc/whisper

ifeq ($(ONNX_OS),linux)
ifeq ($(ONNX_ARCH),x64)
	ALL_GRPC_BACKENDS+=backend-assets/grpc/bark-cpp
	ALL_GRPC_BACKENDS+=backend-assets/grpc/stablediffusion-ggml
endif
endif

ALL_GRPC_BACKENDS+=backend-assets/grpc/local-store
ALL_GRPC_BACKENDS+=backend-assets/grpc/silero-vad
ALL_GRPC_BACKENDS+=$(OPTIONAL_GRPC)
# Use filter-out to remove the specified backends
ALL_GRPC_BACKENDS := $(filter-out $(SKIP_GRPC_BACKEND),$(ALL_GRPC_BACKENDS))

GRPC_BACKENDS?=$(ALL_GRPC_BACKENDS) $(OPTIONAL_GRPC)
TEST_PATHS?=./api/... ./pkg/... ./core/...

# If empty, then we build all
ifeq ($(GRPC_BACKENDS),)
	GRPC_BACKENDS=$(ALL_GRPC_BACKENDS)
endif

ifeq ($(BUILD_API_ONLY),true)
	GRPC_BACKENDS=
endif

.PHONY: all test build vendor get-sources prepare-sources prepare

all: help

## bark.cpp
sources/bark.cpp:
	git clone --recursive $(BARKCPP_REPO) sources/bark.cpp && \
	cd sources/bark.cpp && \
	git checkout $(BARKCPP_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/bark.cpp/build/libbark.a: sources/bark.cpp
	cd sources/bark.cpp && \
	mkdir -p build && \
	cd build && \
	cmake $(CMAKE_ARGS) .. && \
	cmake --build . --config Release

backend/go/bark/libbark.a: sources/bark.cpp/build/libbark.a
	$(MAKE) -C backend/go/bark libbark.a

## go-piper
sources/go-piper:
	mkdir -p sources/go-piper
	cd sources/go-piper && \
	git init && \
	git remote add origin $(PIPER_REPO) && \
	git fetch origin && \
	git checkout $(PIPER_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/go-piper/libpiper_binding.a: sources/go-piper
	$(MAKE) -C sources/go-piper libpiper_binding.a example/main piper.o

## stablediffusion (ggml)
sources/stablediffusion-ggml.cpp:
	git clone --recursive $(STABLEDIFFUSION_GGML_REPO) sources/stablediffusion-ggml.cpp && \
	cd sources/stablediffusion-ggml.cpp && \
	git checkout $(STABLEDIFFUSION_GGML_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

backend/go/image/stablediffusion-ggml/libsd.a: sources/stablediffusion-ggml.cpp
	$(MAKE) -C backend/go/image/stablediffusion-ggml build/libstable-diffusion.a
	$(MAKE) -C backend/go/image/stablediffusion-ggml libsd.a

backend-assets/grpc/stablediffusion-ggml: backend/go/image/stablediffusion-ggml/libsd.a backend-assets/grpc
	$(MAKE) -C backend/go/image/stablediffusion-ggml CGO_LDFLAGS="$(CGO_LDFLAGS)" stablediffusion-ggml

sources/onnxruntime:
	mkdir -p sources/onnxruntime
	curl -L https://github.com/microsoft/onnxruntime/releases/download/v$(ONNX_VERSION)/onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(ONNX_VERSION).tgz -o sources/onnxruntime/onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(ONNX_VERSION).tgz
	cd sources/onnxruntime && tar -xvf onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(ONNX_VERSION).tgz && rm onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(ONNX_VERSION).tgz
	cd sources/onnxruntime && mv onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)-$(ONNX_VERSION)/* ./

backend-assets/lib/libonnxruntime.so.1: backend-assets/lib sources/onnxruntime
	cp -rfv sources/onnxruntime/lib/* backend-assets/lib/
ifeq ($(OS),Darwin)
	mv backend-assets/lib/libonnxruntime.$(ONNX_VERSION).dylib backend-assets/lib/libonnxruntime.dylib
else
	mv backend-assets/lib/libonnxruntime.so.$(ONNX_VERSION) backend-assets/lib/libonnxruntime.so.1
endif

## whisper
sources/whisper.cpp:
	mkdir -p sources/whisper.cpp
	cd sources/whisper.cpp && \
	git init && \
	git remote add origin $(WHISPER_REPO) && \
	git fetch origin && \
	git checkout $(WHISPER_CPP_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/whisper.cpp/build/src/libwhisper.a: sources/whisper.cpp
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	+bash -c "source $(ONEAPI_VARS); \
	cd sources/whisper.cpp && cmake $(CMAKE_ARGS) -DCMAKE_C_COMPILER=icx -DCMAKE_CXX_COMPILER=icpx . -B ./build && \
	cd build && cmake --build . --config Release"
else
	cd sources/whisper.cpp && cmake $(CMAKE_ARGS) . -B ./build
	cd sources/whisper.cpp/build && cmake --build . --config Release
endif

get-sources: sources/go-piper sources/stablediffusion-ggml.cpp sources/bark.cpp sources/whisper.cpp backend/cpp/llama/llama.cpp

replace:
	$(GOCMD) mod edit -replace github.com/ggerganov/whisper.cpp=$(CURDIR)/sources/whisper.cpp
	$(GOCMD) mod edit -replace github.com/ggerganov/whisper.cpp/bindings/go=$(CURDIR)/sources/whisper.cpp/bindings/go
	$(GOCMD) mod edit -replace github.com/mudler/go-piper=$(CURDIR)/sources/go-piper

dropreplace:
	$(GOCMD) mod edit -dropreplace github.com/ggerganov/whisper.cpp
	$(GOCMD) mod edit -dropreplace github.com/ggerganov/whisper.cpp/bindings/go
	$(GOCMD) mod edit -dropreplace github.com/mudler/go-piper

prepare-sources: get-sources replace
	$(GOCMD) mod download

## GENERIC
rebuild: ## Rebuilds the project
	$(GOCMD) clean -cache
	$(MAKE) -C sources/whisper.cpp clean
	$(MAKE) -C sources/go-piper clean
	$(MAKE) build

prepare: prepare-sources $(OPTIONAL_TARGETS)

clean: ## Remove build related file
	$(GOCMD) clean -cache
	rm -f prepare
	rm -rf ./sources
	rm -rf $(BINARY_NAME)
	rm -rf release/
	rm -rf backend-assets/*
	$(MAKE) -C backend/cpp/grpc clean
	$(MAKE) -C backend/go/bark clean
	$(MAKE) -C backend/cpp/llama clean
	$(MAKE) -C backend/go/image/stablediffusion-ggml clean
	rm -rf backend/cpp/llama-* || true
	$(MAKE) dropreplace
	$(MAKE) protogen-clean
	rmdir pkg/grpc/proto || true

clean-tests:
	rm -rf test-models
	rm -rf test-dir
	rm -rf core/http/backend-assets

clean-dc: clean
	cp -r /build/backend-assets /workspace/backend-assets

## Install Go tools
install-go-tools:
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
	go install github.com/GeertJohan/go.rice/rice@latest

## Build:
build: prepare backend-assets grpcs install-go-tools ## Build the project
	$(info ${GREEN}I local-ai build info:${RESET})
	$(info ${GREEN}I BUILD_TYPE: ${YELLOW}$(BUILD_TYPE)${RESET})
	$(info ${GREEN}I GO_TAGS: ${YELLOW}$(GO_TAGS)${RESET})
	$(info ${GREEN}I LD_FLAGS: ${YELLOW}$(LD_FLAGS)${RESET})
	$(info ${GREEN}I UPX: ${YELLOW}$(UPX)${RESET})
ifneq ($(BACKEND_LIBS),)
	$(MAKE) backend-assets/lib
	cp -f $(BACKEND_LIBS) backend-assets/lib/
endif
	rm -rf $(BINARY_NAME) || true
	CGO_LDFLAGS="$(CGO_LDFLAGS)" $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o $(BINARY_NAME) ./
	rice append --exec $(BINARY_NAME)

build-minimal:
	BUILD_GRPC_FOR_BACKEND_LLAMA=true GRPC_BACKENDS="backend-assets/grpc/llama-cpp-avx2" GO_TAGS=p2p $(MAKE) build

build-api:
	BUILD_GRPC_FOR_BACKEND_LLAMA=true BUILD_API_ONLY=true GO_TAGS=p2p $(MAKE) build

backend-assets/lib:
	mkdir -p backend-assets/lib

dist:
	$(MAKE) backend-assets/grpc/llama-cpp-avx2
ifeq ($(DETECT_LIBS),true)
	scripts/prepare-libs.sh backend-assets/grpc/llama-cpp-avx2
endif
ifeq ($(OS),Darwin)
	BUILD_TYPE=none $(MAKE) backend-assets/grpc/llama-cpp-fallback
else
	$(MAKE) backend-assets/grpc/llama-cpp-cuda
	$(MAKE) backend-assets/grpc/llama-cpp-hipblas
	$(MAKE) backend-assets/grpc/llama-cpp-sycl_f16
	$(MAKE) backend-assets/grpc/llama-cpp-sycl_f32
endif
	GO_TAGS="tts p2p" $(MAKE) build
ifeq ($(DETECT_LIBS),true)
	scripts/prepare-libs.sh backend-assets/grpc/piper
endif
	GO_TAGS="tts p2p" STATIC=true $(MAKE) build
	mkdir -p release
# if BUILD_ID is empty, then we don't append it to the binary name
ifeq ($(BUILD_ID),)
	cp $(BINARY_NAME) release/$(BINARY_NAME)-$(OS)-$(ARCH)
	shasum -a 256 release/$(BINARY_NAME)-$(OS)-$(ARCH) > release/$(BINARY_NAME)-$(OS)-$(ARCH).sha256
else
	cp $(BINARY_NAME) release/$(BINARY_NAME)-$(BUILD_ID)-$(OS)-$(ARCH)
	shasum -a 256 release/$(BINARY_NAME)-$(BUILD_ID)-$(OS)-$(ARCH) > release/$(BINARY_NAME)-$(BUILD_ID)-$(OS)-$(ARCH).sha256
endif

dist-cross-linux-arm64:
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_NATIVE=off" GRPC_BACKENDS="backend-assets/grpc/llama-cpp-fallback backend-assets/grpc/llama-cpp-grpc backend-assets/util/llama-cpp-rpc-server" GO_TAGS="p2p" \
	STATIC=true $(MAKE) build
	mkdir -p release
# if BUILD_ID is empty, then we don't append it to the binary name
ifeq ($(BUILD_ID),)
	cp $(BINARY_NAME) release/$(BINARY_NAME)-$(OS)-arm64
	shasum -a 256 release/$(BINARY_NAME)-$(OS)-arm64 > release/$(BINARY_NAME)-$(OS)-arm64.sha256
else
	cp $(BINARY_NAME) release/$(BINARY_NAME)-$(BUILD_ID)-$(OS)-arm64
	shasum -a 256 release/$(BINARY_NAME)-$(BUILD_ID)-$(OS)-arm64 > release/$(BINARY_NAME)-$(BUILD_ID)-$(OS)-arm64.sha256
endif

osx-signed: build
	codesign --deep --force --sign "$(OSX_SIGNING_IDENTITY)" --entitlements "./Entitlements.plist" "./$(BINARY_NAME)"

## Run
run: prepare ## run local-ai
	CGO_LDFLAGS="$(CGO_LDFLAGS)" $(GOCMD) run ./

test-models/testmodel.ggml:
	mkdir test-models
	mkdir test-dir
	wget -q https://huggingface.co/RichardErkhov/Qwen_-_Qwen2-1.5B-Instruct-gguf/resolve/main/Qwen2-1.5B-Instruct.Q2_K.gguf -O test-models/testmodel.ggml
	wget -q https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin -O test-models/whisper-en
	wget -q https://huggingface.co/mudler/all-MiniLM-L6-v2/resolve/main/ggml-model-q4_0.bin -O test-models/bert
	wget -q https://cdn.openai.com/whisper/draft-20220913a/micro-machines.wav -O test-dir/audio.wav
	cp tests/models_fixtures/* test-models

prepare-test: grpcs
	cp -rf backend-assets core/http
	cp tests/models_fixtures/* test-models

## Test targets
test: prepare test-models/testmodel.ggml grpcs
	@echo 'Running tests'
	export GO_TAGS="tts debug"
	$(MAKE) prepare-test
	HUGGINGFACE_GRPC=$(abspath ./)/backend/python/transformers/run.sh TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="!llama-gguf"  --flake-attempts $(TEST_FLAKES) --fail-fast -v -r $(TEST_PATHS)
	$(MAKE) test-llama-gguf
	$(MAKE) test-tts
	$(MAKE) test-stablediffusion

prepare-e2e:
	mkdir -p $(TEST_DIR)
	cp -rfv $(abspath ./tests/e2e-fixtures)/gpu.yaml $(TEST_DIR)/gpu.yaml
	test -e $(TEST_DIR)/ggllm-test-model.bin || wget -q https://huggingface.co/TheBloke/CodeLlama-7B-Instruct-GGUF/resolve/main/codellama-7b-instruct.Q2_K.gguf -O $(TEST_DIR)/ggllm-test-model.bin
	docker build --build-arg GRPC_BACKENDS="$(GRPC_BACKENDS)" --build-arg IMAGE_TYPE=core --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg CUDA_MAJOR_VERSION=12 --build-arg CUDA_MINOR_VERSION=0 --build-arg FFMPEG=true -t localai-tests .

run-e2e-image:
	ls -liah $(abspath ./tests/e2e-fixtures)
	docker run -p 5390:8080 -e MODELS_PATH=/models -e THREADS=1 -e DEBUG=true -d --rm -v $(TEST_DIR):/models --gpus all --name e2e-tests-$(RANDOM) localai-tests

run-e2e-aio: protogen-go
	@echo 'Running e2e AIO tests'
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r ./tests/e2e-aio

test-e2e:
	@echo 'Running e2e tests'
	BUILD_TYPE=$(BUILD_TYPE) \
	LOCALAI_API=http://$(E2E_BRIDGE_IP):5390/v1 \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r ./tests/e2e

teardown-e2e:
	rm -rf $(TEST_DIR) || true
	docker stop $$(docker ps -q --filter ancestor=localai-tests)

test-llama-gguf: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="llama-gguf" --flake-attempts $(TEST_FLAKES) -v -r $(TEST_PATHS)

test-tts: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="tts" --flake-attempts $(TEST_FLAKES) -v -r $(TEST_PATHS)

test-stablediffusion: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="stablediffusion" --flake-attempts $(TEST_FLAKES) -v -r $(TEST_PATHS)

test-stores: backend-assets/grpc/local-store
	mkdir -p tests/integration/backend-assets/grpc
	cp -f backend-assets/grpc/local-store tests/integration/backend-assets/grpc/
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="stores" --flake-attempts $(TEST_FLAKES) -v -r tests/integration

test-container:
	docker build --target requirements -t local-ai-test-container .
	docker run -ti --rm --entrypoint /bin/bash -ti -v $(abspath ./):/build local-ai-test-container

## Help:
help: ## Show this help.
	@echo ''
	@echo 'Usage:'
	@echo '  ${YELLOW}make${RESET} ${GREEN}<target>${RESET}'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} { \
		if (/^[a-zA-Z_-]+:.*?##.*$$/) {printf "    ${YELLOW}%-20s${GREEN}%s${RESET}\n", $$1, $$2} \
		else if (/^## .*$$/) {printf "  ${CYAN}%s${RESET}\n", substr($$1,4)} \
		}' $(MAKEFILE_LIST)

.PHONY: protogen
protogen: protogen-go protogen-python

.PHONY: protogen-clean
protogen-clean: protogen-go-clean protogen-python-clean

.PHONY: protogen-go
protogen-go: install-go-tools
	mkdir -p pkg/grpc/proto
	protoc --experimental_allow_proto3_optional -Ibackend/ --go_out=pkg/grpc/proto/ --go_opt=paths=source_relative --go-grpc_out=pkg/grpc/proto/ --go-grpc_opt=paths=source_relative \
    backend/backend.proto

.PHONY: protogen-go-clean
protogen-go-clean:
	$(RM) pkg/grpc/proto/backend.pb.go pkg/grpc/proto/backend_grpc.pb.go
	$(RM) bin/*

.PHONY: protogen-python
protogen-python: bark-protogen coqui-protogen diffusers-protogen exllama2-protogen rerankers-protogen transformers-protogen kokoro-protogen vllm-protogen faster-whisper-protogen

.PHONY: protogen-python-clean
protogen-python-clean: bark-protogen-clean coqui-protogen-clean diffusers-protogen-clean  exllama2-protogen-clean rerankers-protogen-clean transformers-protogen-clean kokoro-protogen-clean vllm-protogen-clean faster-whisper-protogen-clean

.PHONY: bark-protogen
bark-protogen:
	$(MAKE) -C backend/python/bark protogen

.PHONY: bark-protogen-clean
bark-protogen-clean:
	$(MAKE) -C backend/python/bark protogen-clean

.PHONY: coqui-protogen
coqui-protogen:
	$(MAKE) -C backend/python/coqui protogen

.PHONY: coqui-protogen-clean
coqui-protogen-clean:
	$(MAKE) -C backend/python/coqui protogen-clean

.PHONY: diffusers-protogen
diffusers-protogen:
	$(MAKE) -C backend/python/diffusers protogen

.PHONY: diffusers-protogen-clean
diffusers-protogen-clean:
	$(MAKE) -C backend/python/diffusers protogen-clean

.PHONY: faster-whisper-protogen
faster-whisper-protogen:
	$(MAKE) -C backend/python/faster-whisper protogen

.PHONY: faster-whisper-protogen-clean
faster-whisper-protogen-clean:
	$(MAKE) -C backend/python/faster-whisper protogen-clean

.PHONY: exllama2-protogen
exllama2-protogen:
	$(MAKE) -C backend/python/exllama2 protogen

.PHONY: exllama2-protogen-clean
exllama2-protogen-clean:
	$(MAKE) -C backend/python/exllama2 protogen-clean

.PHONY: rerankers-protogen
rerankers-protogen:
	$(MAKE) -C backend/python/rerankers protogen

.PHONY: rerankers-protogen-clean
rerankers-protogen-clean:
	$(MAKE) -C backend/python/rerankers protogen-clean

.PHONY: transformers-protogen
transformers-protogen:
	$(MAKE) -C backend/python/transformers protogen

.PHONY: transformers-protogen-clean
transformers-protogen-clean:
	$(MAKE) -C backend/python/transformers protogen-clean

.PHONY: kokoro-protogen
kokoro-protogen:
	$(MAKE) -C backend/python/kokoro protogen

.PHONY: kokoro-protogen-clean
kokoro-protogen-clean:
	$(MAKE) -C backend/python/kokoro protogen-clean

.PHONY: vllm-protogen
vllm-protogen:
	$(MAKE) -C backend/python/vllm protogen

.PHONY: vllm-protogen-clean
vllm-protogen-clean:
	$(MAKE) -C backend/python/vllm protogen-clean

## GRPC
# Note: it is duplicated in the Dockerfile
prepare-extra-conda-environments: protogen-python
	$(MAKE) -C backend/python/bark
	$(MAKE) -C backend/python/coqui
	$(MAKE) -C backend/python/diffusers
	$(MAKE) -C backend/python/faster-whisper
	$(MAKE) -C backend/python/vllm
	$(MAKE) -C backend/python/rerankers
	$(MAKE) -C backend/python/transformers
	$(MAKE) -C backend/python/kokoro
	$(MAKE) -C backend/python/exllama2

prepare-test-extra: protogen-python
	$(MAKE) -C backend/python/transformers
	$(MAKE) -C backend/python/diffusers
	$(MAKE) -C backend/python/vllm

test-extra: prepare-test-extra
	$(MAKE) -C backend/python/transformers test
	$(MAKE) -C backend/python/diffusers test
	$(MAKE) -C backend/python/vllm test

backend-assets:
	mkdir -p backend-assets
ifeq ($(BUILD_API_ONLY),true)
	touch backend-assets/keep
endif

backend-assets/espeak-ng-data: sources/go-piper sources/go-piper/libpiper_binding.a
	mkdir -p backend-assets/espeak-ng-data
	@cp -rf sources/go-piper/piper-phonemize/pi/share/espeak-ng-data/. backend-assets/espeak-ng-data

backend-assets/grpc: protogen-go replace
	mkdir -p backend-assets/grpc

backend-assets/grpc/huggingface: backend-assets/grpc
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/huggingface ./backend/go/llm/langchain/
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/huggingface
endif

backend/cpp/llama/llama.cpp:
	LLAMA_VERSION=$(CPPLLAMA_VERSION) $(MAKE) -C backend/cpp/llama llama.cpp

INSTALLED_PACKAGES=$(CURDIR)/backend/cpp/grpc/installed_packages
INSTALLED_LIB_CMAKE=$(INSTALLED_PACKAGES)/lib/cmake
ADDED_CMAKE_ARGS=-Dabsl_DIR=${INSTALLED_LIB_CMAKE}/absl \
				 -DProtobuf_DIR=${INSTALLED_LIB_CMAKE}/protobuf \
				 -Dutf8_range_DIR=${INSTALLED_LIB_CMAKE}/utf8_range \
				 -DgRPC_DIR=${INSTALLED_LIB_CMAKE}/grpc \
				 -DCMAKE_CXX_STANDARD_INCLUDE_DIRECTORIES=${INSTALLED_PACKAGES}/include
build-llama-cpp-grpc-server:
# Conditionally build grpc for the llama backend to use if needed
ifdef BUILD_GRPC_FOR_BACKEND_LLAMA
	$(MAKE) -C backend/cpp/grpc build
	_PROTOBUF_PROTOC=${INSTALLED_PACKAGES}/bin/proto \
	_GRPC_CPP_PLUGIN_EXECUTABLE=${INSTALLED_PACKAGES}/bin/grpc_cpp_plugin \
	PATH="${INSTALLED_PACKAGES}/bin:${PATH}" \
	CMAKE_ARGS="${CMAKE_ARGS} ${ADDED_CMAKE_ARGS}" \
	LLAMA_VERSION=$(CPPLLAMA_VERSION) \
	$(MAKE) -C backend/cpp/${VARIANT} grpc-server
else
	echo "BUILD_GRPC_FOR_BACKEND_LLAMA is not defined."
	LLAMA_VERSION=$(CPPLLAMA_VERSION) $(MAKE) -C backend/cpp/${VARIANT} grpc-server
endif

# This target is for manually building a variant with-auto detected flags
backend-assets/grpc/llama-cpp: backend-assets/grpc backend/cpp/llama/llama.cpp
	cp -rf backend/cpp/llama backend/cpp/llama-cpp
	$(MAKE) -C backend/cpp/llama-cpp purge
	$(info ${GREEN}I llama-cpp build info:avx2${RESET})
	$(MAKE) VARIANT="llama-cpp" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cpp/grpc-server backend-assets/grpc/llama-cpp

backend-assets/grpc/llama-cpp-avx2: backend-assets/grpc backend/cpp/llama/llama.cpp
	cp -rf backend/cpp/llama backend/cpp/llama-avx2
	$(MAKE) -C backend/cpp/llama-avx2 purge
	$(info ${GREEN}I llama-cpp build info:avx2${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) VARIANT="llama-avx2" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-avx2/grpc-server backend-assets/grpc/llama-cpp-avx2

backend-assets/grpc/llama-cpp-avx512: backend-assets/grpc backend/cpp/llama/llama.cpp
	cp -rf backend/cpp/llama backend/cpp/llama-avx512
	$(MAKE) -C backend/cpp/llama-avx512 purge
	$(info ${GREEN}I llama-cpp build info:avx512${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) VARIANT="llama-avx512" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-avx512/grpc-server backend-assets/grpc/llama-cpp-avx512

backend-assets/grpc/llama-cpp-avx: backend-assets/grpc backend/cpp/llama/llama.cpp
	cp -rf backend/cpp/llama backend/cpp/llama-avx
	$(MAKE) -C backend/cpp/llama-avx purge
	$(info ${GREEN}I llama-cpp build info:avx${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off" $(MAKE) VARIANT="llama-avx" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-avx/grpc-server backend-assets/grpc/llama-cpp-avx

backend-assets/grpc/llama-cpp-fallback: backend-assets/grpc backend/cpp/llama/llama.cpp
	cp -rf backend/cpp/llama backend/cpp/llama-fallback
	$(MAKE) -C backend/cpp/llama-fallback purge
	$(info ${GREEN}I llama-cpp build info:fallback${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off" $(MAKE) VARIANT="llama-fallback" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-fallback/grpc-server backend-assets/grpc/llama-cpp-fallback

backend-assets/grpc/llama-cpp-cuda: backend-assets/grpc backend/cpp/llama/llama.cpp
	cp -rf backend/cpp/llama backend/cpp/llama-cuda
	$(MAKE) -C backend/cpp/llama-cuda purge
	$(info ${GREEN}I llama-cpp build info:cuda${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_CUDA=ON" $(MAKE) VARIANT="llama-cuda" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-cuda/grpc-server backend-assets/grpc/llama-cpp-cuda

backend-assets/grpc/llama-cpp-hipblas: backend-assets/grpc backend/cpp/llama/llama.cpp
	cp -rf backend/cpp/llama backend/cpp/llama-hipblas
	$(MAKE) -C backend/cpp/llama-hipblas purge
	$(info ${GREEN}I llama-cpp build info:hipblas${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off" BUILD_TYPE="hipblas" $(MAKE) VARIANT="llama-hipblas" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-hipblas/grpc-server backend-assets/grpc/llama-cpp-hipblas

backend-assets/grpc/llama-cpp-sycl_f16: backend-assets/grpc backend/cpp/llama/llama.cpp
	cp -rf backend/cpp/llama backend/cpp/llama-sycl_f16
	$(MAKE) -C backend/cpp/llama-sycl_f16 purge
	$(info ${GREEN}I llama-cpp build info:sycl_f16${RESET})
	BUILD_TYPE="sycl_f16" $(MAKE) VARIANT="llama-sycl_f16" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-sycl_f16/grpc-server backend-assets/grpc/llama-cpp-sycl_f16

backend-assets/grpc/llama-cpp-sycl_f32: backend-assets/grpc backend/cpp/llama/llama.cpp
	cp -rf backend/cpp/llama backend/cpp/llama-sycl_f32
	$(MAKE) -C backend/cpp/llama-sycl_f32 purge
	$(info ${GREEN}I llama-cpp build info:sycl_f32${RESET})
	BUILD_TYPE="sycl_f32" $(MAKE) VARIANT="llama-sycl_f32" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-sycl_f32/grpc-server backend-assets/grpc/llama-cpp-sycl_f32

backend-assets/grpc/llama-cpp-grpc: backend-assets/grpc backend/cpp/llama/llama.cpp
	cp -rf backend/cpp/llama backend/cpp/llama-grpc
	$(MAKE) -C backend/cpp/llama-grpc purge
	$(info ${GREEN}I llama-cpp build info:grpc${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off" TARGET="--target grpc-server --target rpc-server" $(MAKE) VARIANT="llama-grpc" build-llama-cpp-grpc-server
	cp -rfv backend/cpp/llama-grpc/grpc-server backend-assets/grpc/llama-cpp-grpc

backend-assets/util/llama-cpp-rpc-server: backend-assets/grpc/llama-cpp-grpc
	mkdir -p backend-assets/util/
	cp -rf backend/cpp/llama-grpc/llama.cpp/build/bin/rpc-server backend-assets/util/llama-cpp-rpc-server

backend-assets/grpc/bark-cpp: backend/go/bark/libbark.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(CURDIR)/backend/go/bark/ LIBRARY_PATH=$(CURDIR)/backend/go/bark/ \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/bark-cpp ./backend/go/bark/
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/bark-cpp
endif

backend-assets/grpc/piper: sources/go-piper sources/go-piper/libpiper_binding.a backend-assets/grpc backend-assets/espeak-ng-data
	CGO_CXXFLAGS="$(PIPER_CGO_CXXFLAGS)" CGO_LDFLAGS="$(PIPER_CGO_LDFLAGS)" LIBRARY_PATH=$(CURDIR)/sources/go-piper \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/piper ./backend/go/tts/
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/piper
endif

backend-assets/grpc/silero-vad: backend-assets/grpc backend-assets/lib/libonnxruntime.so.1
	CGO_LDFLAGS="$(CGO_LDFLAGS)" CPATH="$(CPATH):$(CURDIR)/sources/onnxruntime/include/" LIBRARY_PATH=$(CURDIR)/backend-assets/lib \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/silero-vad ./backend/go/vad/silero
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/silero-vad
endif

backend-assets/grpc/whisper: sources/whisper.cpp sources/whisper.cpp/build/src/libwhisper.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS) $(CGO_LDFLAGS_WHISPER)" C_INCLUDE_PATH="${WHISPER_INCLUDE_PATH}" LIBRARY_PATH="${WHISPER_LIBRARY_PATH}" LD_LIBRARY_PATH="${WHISPER_LIBRARY_PATH}" \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/whisper ./backend/go/transcribe/whisper
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/whisper
endif

backend-assets/grpc/local-store: backend-assets/grpc
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/local-store ./backend/go/stores/
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/local-store
endif

grpcs: prepare $(GRPC_BACKENDS)

DOCKER_IMAGE?=local-ai
DOCKER_AIO_IMAGE?=local-ai-aio
IMAGE_TYPE?=core
BASE_IMAGE?=ubuntu:22.04

docker:
	docker build \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="$(GO_TAGS)" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		--build-arg BUILD_TYPE=$(BUILD_TYPE) \
		-t $(DOCKER_IMAGE) .

docker-cuda11:
	docker build \
		--build-arg CUDA_MAJOR_VERSION=11 \
		--build-arg CUDA_MINOR_VERSION=8 \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="$(GO_TAGS)" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		--build-arg BUILD_TYPE=$(BUILD_TYPE) \
		-t $(DOCKER_IMAGE)-cuda11 .

docker-aio:
	@echo "Building AIO image with base $(BASE_IMAGE) as $(DOCKER_AIO_IMAGE)"
	docker build \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		-t $(DOCKER_AIO_IMAGE) -f Dockerfile.aio .

docker-aio-all:
	$(MAKE) docker-aio DOCKER_AIO_SIZE=cpu
	$(MAKE) docker-aio DOCKER_AIO_SIZE=cpu

docker-image-intel:
	docker build \
		--progress plain \
		--build-arg BASE_IMAGE=intel/oneapi-basekit:2025.1.0-0-devel-ubuntu24.04 \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="none" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		--build-arg BUILD_TYPE=sycl_f32 -t $(DOCKER_IMAGE) .

docker-image-intel-xpu:
	docker build \
		--build-arg BASE_IMAGE=intel/oneapi-basekit:2025.1.0-0-devel-ubuntu22.04 \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="none" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		--build-arg BUILD_TYPE=sycl_f32 -t $(DOCKER_IMAGE) .

.PHONY: swagger
swagger:
	swag init -g core/http/app.go --output swagger

.PHONY: gen-assets
gen-assets:
	$(GOCMD) run core/dependencies_manager/manager.go webui_static.yaml core/http/static/assets

## Documentation
docs/layouts/_default:
	mkdir -p docs/layouts/_default

docs/static/gallery.html: docs/layouts/_default
	$(GOCMD) run ./.github/ci/modelslist.go ./gallery/index.yaml > docs/static/gallery.html

docs/public: docs/layouts/_default docs/static/gallery.html
	cd docs && hugo --minify

docs-clean:
	rm -rf docs/public
	rm -rf docs/static/gallery.html

.PHONY: docs
docs: docs/static/gallery.html
	cd docs && hugo serve
