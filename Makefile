GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_NAME=local-ai

DETECT_LIBS?=true

# llama.cpp versions
GOLLAMA_REPO?=https://github.com/go-skynet/go-llama.cpp
GOLLAMA_VERSION?=2b57a8ae43e4699d3dc5d1496a1ccd42922993be
CPPLLAMA_VERSION?=e11bd856d538e44d24d8cad4b0381fba0984d162

# go-rwkv version
RWKV_REPO?=https://github.com/donomii/go-rwkv.cpp
RWKV_VERSION?=661e7ae26d442f5cfebd2a0881b44e8c55949ec6

# whisper.cpp version
WHISPER_REPO?=https://github.com/ggerganov/whisper.cpp
WHISPER_CPP_VERSION?=9e3c5345cd46ea718209db53464e426c3fe7a25e

# bert.cpp version
BERT_REPO?=https://github.com/go-skynet/go-bert.cpp
BERT_VERSION?=710044b124545415f555e4260d16b146c725a6e4

# go-piper version
PIPER_REPO?=https://github.com/mudler/go-piper
PIPER_VERSION?=9d0100873a7dbb0824dfea40e8cec70a1b110759

# stablediffusion version
STABLEDIFFUSION_REPO?=https://github.com/mudler/go-stable-diffusion
STABLEDIFFUSION_VERSION?=4a3cd6aeae6f66ee57eae9a0075f8c58c3a6a38f

# tinydream version
TINYDREAM_REPO?=https://github.com/M0Rf30/go-tiny-dream
TINYDREAM_VERSION?=c04fa463ace9d9a6464313aa5f9cd0f953b6c057

export BUILD_TYPE?=
export STABLE_BUILD_TYPE?=$(BUILD_TYPE)
export CMAKE_ARGS?=
export BACKEND_LIBS?=

CGO_LDFLAGS?=
CGO_LDFLAGS_WHISPER?=
CGO_LDFLAGS_WHISPER+=-lggml
CUDA_LIBPATH?=/usr/local/cuda/lib64/
GO_TAGS?=
BUILD_ID?=

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

ifeq ($(OS),Darwin)

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
	endif
else
CGO_LDFLAGS_WHISPER+=-lgomp
endif

ifeq ($(BUILD_TYPE),openblas)
	CGO_LDFLAGS+=-lopenblas
	export GGML_OPENBLAS=1
endif

ifeq ($(BUILD_TYPE),cublas)
	CGO_LDFLAGS+=-lcublas -lcudart -L$(CUDA_LIBPATH)
	export GGML_CUDA=1
	CGO_LDFLAGS_WHISPER+=-L$(CUDA_LIBPATH)/stubs/ -lcuda -lcufft
endif

ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=1
endif

ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	export GGML_SYCL=1
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	export GGML_SYCL_F16=1
endif

ifeq ($(BUILD_TYPE),hipblas)
	ROCM_HOME ?= /opt/rocm
	ROCM_PATH ?= /opt/rocm
	LD_LIBRARY_PATH ?= /opt/rocm/lib:/opt/rocm/llvm/lib
	export CXX=$(ROCM_HOME)/llvm/bin/clang++
	export CC=$(ROCM_HOME)/llvm/bin/clang
	# llama-ggml has no hipblas support, so override it here.
	export STABLE_BUILD_TYPE=
	export GGML_HIPBLAS=1
	GPU_TARGETS ?= gfx900,gfx906,gfx908,gfx940,gfx941,gfx942,gfx90a,gfx1030,gfx1031,gfx1100,gfx1101
	AMDGPU_TARGETS ?= "$(GPU_TARGETS)"
	CMAKE_ARGS+=-DGGML_HIPBLAS=ON -DAMDGPU_TARGETS="$(AMDGPU_TARGETS)" -DGPU_TARGETS="$(GPU_TARGETS)"
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

ifeq ($(findstring stablediffusion,$(GO_TAGS)),stablediffusion)
#	OPTIONAL_TARGETS+=go-stable-diffusion/libstablediffusion.a
	OPTIONAL_GRPC+=backend-assets/grpc/stablediffusion
endif

ifeq ($(findstring tinydream,$(GO_TAGS)),tinydream)
#	OPTIONAL_TARGETS+=go-tiny-dream/libtinydream.a
	OPTIONAL_GRPC+=backend-assets/grpc/tinydream
endif

ifeq ($(findstring tts,$(GO_TAGS)),tts)
#	OPTIONAL_TARGETS+=go-piper/libpiper_binding.a
#	OPTIONAL_TARGETS+=backend-assets/espeak-ng-data
	PIPER_CGO_CXXFLAGS+=-I$(CURDIR)/sources/go-piper/piper/src/cpp -I$(CURDIR)/sources/go-piper/piper/build/fi/include -I$(CURDIR)/sources/go-piper/piper/build/pi/include -I$(CURDIR)/sources/go-piper/piper/build/si/include
	PIPER_CGO_LDFLAGS+=-L$(CURDIR)/sources/go-piper/piper/build/fi/lib -L$(CURDIR)/sources/go-piper/piper/build/pi/lib -L$(CURDIR)/sources/go-piper/piper/build/si/lib -lfmt -lspdlog -lucd
	OPTIONAL_GRPC+=backend-assets/grpc/piper
endif

ALL_GRPC_BACKENDS=backend-assets/grpc/huggingface
ALL_GRPC_BACKENDS+=backend-assets/grpc/bert-embeddings
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-cpp-avx
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-cpp-avx2
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-cpp-fallback
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-ggml
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-cpp-grpc
ALL_GRPC_BACKENDS+=backend-assets/util/llama-cpp-rpc-server
ALL_GRPC_BACKENDS+=backend-assets/grpc/rwkv
ALL_GRPC_BACKENDS+=backend-assets/grpc/whisper
ALL_GRPC_BACKENDS+=backend-assets/grpc/local-store
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

## BERT embeddings
sources/go-bert.cpp:
	mkdir -p sources/go-bert.cpp
	cd sources/go-bert.cpp && \
	git init && \
	git remote add origin $(BERT_REPO) && \
	git fetch origin && \
	git checkout $(BERT_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/go-bert.cpp/libgobert.a: sources/go-bert.cpp
	$(MAKE) -C sources/go-bert.cpp libgobert.a

## go-llama.cpp
sources/go-llama.cpp:
	mkdir -p sources/go-llama.cpp
	cd sources/go-llama.cpp && \
	git init && \
	git remote add origin $(GOLLAMA_REPO) && \
	git fetch origin && \
	git checkout $(GOLLAMA_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/go-llama.cpp/libbinding.a: sources/go-llama.cpp
	$(MAKE) -C sources/go-llama.cpp BUILD_TYPE=$(STABLE_BUILD_TYPE) libbinding.a

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


## RWKV
sources/go-rwkv.cpp:
	mkdir -p sources/go-rwkv.cpp
	cd sources/go-rwkv.cpp && \
	git init && \
	git remote add origin $(RWKV_REPO) && \
	git fetch origin && \
	git checkout $(RWKV_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/go-rwkv.cpp/librwkv.a: sources/go-rwkv.cpp
	cd sources/go-rwkv.cpp && cd rwkv.cpp &&	cmake . -DRWKV_BUILD_SHARED_LIBRARY=OFF &&	cmake --build . && 	cp librwkv.a ..

## stable diffusion
sources/go-stable-diffusion:
	mkdir -p sources/go-stable-diffusion
	cd sources/go-stable-diffusion && \
	git init && \
	git remote add origin $(STABLEDIFFUSION_REPO) && \
	git fetch origin && \
	git checkout $(STABLEDIFFUSION_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/go-stable-diffusion/libstablediffusion.a: sources/go-stable-diffusion
	CPATH="$(CPATH):/usr/include/opencv4" $(MAKE) -C sources/go-stable-diffusion libstablediffusion.a

## tiny-dream
sources/go-tiny-dream:
	mkdir -p sources/go-tiny-dream
	cd sources/go-tiny-dream && \
	git init && \
	git remote add origin $(TINYDREAM_REPO) && \
	git fetch origin && \
	git checkout $(TINYDREAM_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/go-tiny-dream/libtinydream.a: sources/go-tiny-dream
	$(MAKE) -C sources/go-tiny-dream libtinydream.a

## whisper
sources/whisper.cpp:
	mkdir -p sources/whisper.cpp
	cd sources/whisper.cpp && \
	git init && \
	git remote add origin $(WHISPER_REPO) && \
	git fetch origin && \
	git checkout $(WHISPER_CPP_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/whisper.cpp/libwhisper.a: sources/whisper.cpp
	cd sources/whisper.cpp && $(MAKE) libwhisper.a libggml.a

get-sources: sources/go-llama.cpp sources/go-piper sources/go-rwkv.cpp sources/whisper.cpp sources/go-bert.cpp sources/go-stable-diffusion sources/go-tiny-dream backend/cpp/llama/llama.cpp

replace:
	$(GOCMD) mod edit -replace github.com/donomii/go-rwkv.cpp=$(CURDIR)/sources/go-rwkv.cpp
	$(GOCMD) mod edit -replace github.com/ggerganov/whisper.cpp=$(CURDIR)/sources/whisper.cpp
	$(GOCMD) mod edit -replace github.com/ggerganov/whisper.cpp/bindings/go=$(CURDIR)/sources/whisper.cpp/bindings/go
	$(GOCMD) mod edit -replace github.com/go-skynet/go-bert.cpp=$(CURDIR)/sources/go-bert.cpp
	$(GOCMD) mod edit -replace github.com/M0Rf30/go-tiny-dream=$(CURDIR)/sources/go-tiny-dream
	$(GOCMD) mod edit -replace github.com/mudler/go-piper=$(CURDIR)/sources/go-piper
	$(GOCMD) mod edit -replace github.com/mudler/go-stable-diffusion=$(CURDIR)/sources/go-stable-diffusion
	$(GOCMD) mod edit -replace github.com/go-skynet/go-llama.cpp=$(CURDIR)/sources/go-llama.cpp

dropreplace:
	$(GOCMD) mod edit -dropreplace github.com/donomii/go-rwkv.cpp
	$(GOCMD) mod edit -dropreplace github.com/ggerganov/whisper.cpp
	$(GOCMD) mod edit -dropreplace github.com/ggerganov/whisper.cpp/bindings/go
	$(GOCMD) mod edit -dropreplace github.com/go-skynet/go-bert.cpp
	$(GOCMD) mod edit -dropreplace github.com/M0Rf30/go-tiny-dream
	$(GOCMD) mod edit -dropreplace github.com/mudler/go-piper
	$(GOCMD) mod edit -dropreplace github.com/mudler/go-stable-diffusion
	$(GOCMD) mod edit -dropreplace github.com/go-skynet/go-llama.cpp

prepare-sources: get-sources replace
	$(GOCMD) mod download

## GENERIC
rebuild: ## Rebuilds the project
	$(GOCMD) clean -cache
	$(MAKE) -C sources/go-llama.cpp clean
	$(MAKE) -C sources/go-rwkv.cpp clean
	$(MAKE) -C sources/whisper.cpp clean
	$(MAKE) -C sources/go-stable-diffusion clean
	$(MAKE) -C sources/go-bert.cpp clean
	$(MAKE) -C sources/go-piper clean
	$(MAKE) -C sources/go-tiny-dream clean
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
	$(MAKE) -C backend/cpp/llama clean
	rm -rf backend/cpp/llama-* || true
	$(MAKE) dropreplace
	$(MAKE) protogen-clean
	rmdir pkg/grpc/proto || true

clean-tests:
	rm -rf test-models
	rm -rf test-dir
	rm -rf core/http/backend-assets

## Build:
build: prepare backend-assets grpcs ## Build the project
	$(info ${GREEN}I local-ai build info:${RESET})
	$(info ${GREEN}I BUILD_TYPE: ${YELLOW}$(BUILD_TYPE)${RESET})
	$(info ${GREEN}I GO_TAGS: ${YELLOW}$(GO_TAGS)${RESET})
	$(info ${GREEN}I LD_FLAGS: ${YELLOW}$(LD_FLAGS)${RESET})
	$(info ${GREEN}I UPX: ${YELLOW}$(UPX)${RESET})
ifneq ($(BACKEND_LIBS),)
	$(MAKE) backend-assets/lib
	cp -f $(BACKEND_LIBS) backend-assets/lib/
endif
	CGO_LDFLAGS="$(CGO_LDFLAGS)" $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o $(BINARY_NAME) ./

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
	wget -q https://huggingface.co/TheBloke/orca_mini_3B-GGML/resolve/main/orca-mini-3b.ggmlv3.q4_0.bin -O test-models/testmodel.ggml
	wget -q https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin -O test-models/whisper-en
	wget -q https://huggingface.co/mudler/all-MiniLM-L6-v2/resolve/main/ggml-model-q4_0.bin -O test-models/bert
	wget -q https://cdn.openai.com/whisper/draft-20220913a/micro-machines.wav -O test-dir/audio.wav
	wget -q https://huggingface.co/mudler/rwkv-4-raven-1.5B-ggml/resolve/main/RWKV-4-Raven-1B5-v11-Eng99%2525-Other1%2525-20230425-ctx4096_Q4_0.bin -O test-models/rwkv
	wget -q https://raw.githubusercontent.com/saharNooby/rwkv.cpp/5eb8f09c146ea8124633ab041d9ea0b1f1db4459/rwkv/20B_tokenizer.json -O test-models/rwkv.tokenizer.json
	cp tests/models_fixtures/* test-models

prepare-test: grpcs
	cp -rf backend-assets core/http
	cp tests/models_fixtures/* test-models

test: prepare test-models/testmodel.ggml grpcs
	@echo 'Running tests'
	export GO_TAGS="tts stablediffusion debug"
	$(MAKE) prepare-test
	HUGGINGFACE_GRPC=$(abspath ./)/backend/python/sentencetransformers/run.sh TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="!llama && !llama-gguf"  --flake-attempts $(TEST_FLAKES) --fail-fast -v -r $(TEST_PATHS)
	$(MAKE) test-llama
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

run-e2e-aio:
	@echo 'Running e2e AIO tests'
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts 5 -v -r ./tests/e2e-aio

test-e2e:
	@echo 'Running e2e tests'
	BUILD_TYPE=$(BUILD_TYPE) \
	LOCALAI_API=http://$(E2E_BRIDGE_IP):5390/v1 \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts 5 -v -r ./tests/e2e

teardown-e2e:
	rm -rf $(TEST_DIR) || true
	docker stop $$(docker ps -q --filter ancestor=localai-tests)

test-llama: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="llama" --flake-attempts 5 -v -r $(TEST_PATHS)

test-llama-gguf: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="llama-gguf" --flake-attempts 5 -v -r $(TEST_PATHS)

test-tts: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="tts" --flake-attempts 1 -v -r $(TEST_PATHS)

test-stablediffusion: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="stablediffusion" --flake-attempts 1 -v -r $(TEST_PATHS)

test-stores: backend-assets/grpc/local-store
	mkdir -p tests/integration/backend-assets/grpc
	cp -f backend-assets/grpc/local-store tests/integration/backend-assets/grpc/
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="stores" --flake-attempts 1 -v -r tests/integration

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
protogen-go:
	mkdir -p pkg/grpc/proto
	protoc --experimental_allow_proto3_optional -Ibackend/ --go_out=pkg/grpc/proto/ --go_opt=paths=source_relative --go-grpc_out=pkg/grpc/proto/ --go-grpc_opt=paths=source_relative \
    backend/backend.proto

.PHONY: protogen-go-clean
protogen-go-clean:
	$(RM) pkg/grpc/proto/backend.pb.go pkg/grpc/proto/backend_grpc.pb.go
	$(RM) bin/*

.PHONY: protogen-python
protogen-python: autogptq-protogen bark-protogen coqui-protogen diffusers-protogen exllama-protogen exllama2-protogen mamba-protogen rerankers-protogen sentencetransformers-protogen transformers-protogen parler-tts-protogen transformers-musicgen-protogen vall-e-x-protogen vllm-protogen openvoice-protogen

.PHONY: protogen-python-clean
protogen-python-clean: autogptq-protogen-clean bark-protogen-clean coqui-protogen-clean diffusers-protogen-clean exllama-protogen-clean exllama2-protogen-clean mamba-protogen-clean sentencetransformers-protogen-clean rerankers-protogen-clean transformers-protogen-clean transformers-musicgen-protogen-clean parler-tts-protogen-clean vall-e-x-protogen-clean vllm-protogen-clean openvoice-protogen-clean

.PHONY: autogptq-protogen
autogptq-protogen:
	$(MAKE) -C backend/python/autogptq protogen

.PHONY: autogptq-protogen-clean
autogptq-protogen-clean:
	$(MAKE) -C backend/python/autogptq protogen-clean

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

.PHONY: exllama-protogen
exllama-protogen:
	$(MAKE) -C backend/python/exllama protogen

.PHONY: exllama-protogen-clean
exllama-protogen-clean:
	$(MAKE) -C backend/python/exllama protogen-clean

.PHONY: exllama2-protogen
exllama2-protogen:
	$(MAKE) -C backend/python/exllama2 protogen

.PHONY: exllama2-protogen-clean
exllama2-protogen-clean:
	$(MAKE) -C backend/python/exllama2 protogen-clean

.PHONY: mamba-protogen
mamba-protogen:
	$(MAKE) -C backend/python/mamba protogen

.PHONY: mamba-protogen-clean
mamba-protogen-clean:
	$(MAKE) -C backend/python/mamba protogen-clean

.PHONY: rerankers-protogen
rerankers-protogen:
	$(MAKE) -C backend/python/rerankers protogen

.PHONY: rerankers-protogen-clean
rerankers-protogen-clean:
	$(MAKE) -C backend/python/rerankers protogen-clean

.PHONY: sentencetransformers-protogen
sentencetransformers-protogen:
	$(MAKE) -C backend/python/sentencetransformers protogen

.PHONY: sentencetransformers-protogen-clean
sentencetransformers-protogen-clean:
	$(MAKE) -C backend/python/sentencetransformers protogen-clean

.PHONY: transformers-protogen
transformers-protogen:
	$(MAKE) -C backend/python/transformers protogen

.PHONY: transformers-protogen-clean
transformers-protogen-clean:
	$(MAKE) -C backend/python/transformers protogen-clean

.PHONY: parler-tts-protogen
parler-tts-protogen:
	$(MAKE) -C backend/python/parler-tts protogen

.PHONY: parler-tts-protogen-clean
parler-tts-protogen-clean:
	$(MAKE) -C backend/python/parler-tts protogen-clean

.PHONY: transformers-musicgen-protogen
transformers-musicgen-protogen:
	$(MAKE) -C backend/python/transformers-musicgen protogen

.PHONY: transformers-musicgen-protogen-clean
transformers-musicgen-protogen-clean:
	$(MAKE) -C backend/python/transformers-musicgen protogen-clean

.PHONY: vall-e-x-protogen
vall-e-x-protogen:
	$(MAKE) -C backend/python/vall-e-x protogen

.PHONY: vall-e-x-protogen-clean
vall-e-x-protogen-clean:
	$(MAKE) -C backend/python/vall-e-x protogen-clean

.PHONY: openvoice-protogen
openvoice-protogen:
	$(MAKE) -C backend/python/openvoice protogen

.PHONY: openvoice-protogen-clean
openvoice-protogen-clean:
	$(MAKE) -C backend/python/openvoice protogen-clean

.PHONY: vllm-protogen
vllm-protogen:
	$(MAKE) -C backend/python/vllm protogen

.PHONY: vllm-protogen-clean
vllm-protogen-clean:
	$(MAKE) -C backend/python/vllm protogen-clean

## GRPC
# Note: it is duplicated in the Dockerfile
prepare-extra-conda-environments: protogen-python
	$(MAKE) -C backend/python/autogptq
	$(MAKE) -C backend/python/bark
	$(MAKE) -C backend/python/coqui
	$(MAKE) -C backend/python/diffusers
	$(MAKE) -C backend/python/vllm
	$(MAKE) -C backend/python/mamba
	$(MAKE) -C backend/python/sentencetransformers
	$(MAKE) -C backend/python/rerankers
	$(MAKE) -C backend/python/transformers
	$(MAKE) -C backend/python/transformers-musicgen
	$(MAKE) -C backend/python/parler-tts
	$(MAKE) -C backend/python/vall-e-x
	$(MAKE) -C backend/python/openvoice
	$(MAKE) -C backend/python/exllama
	$(MAKE) -C backend/python/exllama2

prepare-test-extra: protogen-python
	$(MAKE) -C backend/python/transformers
	$(MAKE) -C backend/python/diffusers

test-extra: prepare-test-extra
	$(MAKE) -C backend/python/transformers test
	$(MAKE) -C backend/python/diffusers test

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

backend-assets/grpc/bert-embeddings: sources/go-bert.cpp sources/go-bert.cpp/libgobert.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(CURDIR)/sources/go-bert.cpp LIBRARY_PATH=$(CURDIR)/sources/go-bert.cpp \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/bert-embeddings ./backend/go/llm/bert/
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/bert-embeddings
endif

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
# TODO: every binary should have its own folder instead, so can have different metal implementations
ifeq ($(BUILD_TYPE),metal)
	cp backend/cpp/llama-fallback/llama.cpp/build/bin/default.metallib backend-assets/grpc/
endif

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
	BUILD_TYPE="hipblas" $(MAKE) VARIANT="llama-hipblas" build-llama-cpp-grpc-server
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

backend-assets/grpc/llama-ggml: sources/go-llama.cpp sources/go-llama.cpp/libbinding.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(CURDIR)/sources/go-llama.cpp LIBRARY_PATH=$(CURDIR)/sources/go-llama.cpp \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/llama-ggml ./backend/go/llm/llama-ggml/
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/llama-ggml
endif

backend-assets/grpc/piper: sources/go-piper sources/go-piper/libpiper_binding.a backend-assets/grpc backend-assets/espeak-ng-data
	CGO_CXXFLAGS="$(PIPER_CGO_CXXFLAGS)" CGO_LDFLAGS="$(PIPER_CGO_LDFLAGS)" LIBRARY_PATH=$(CURDIR)/sources/go-piper \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/piper ./backend/go/tts/
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/piper
endif

backend-assets/grpc/rwkv: sources/go-rwkv.cpp sources/go-rwkv.cpp/librwkv.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(CURDIR)/sources/go-rwkv.cpp LIBRARY_PATH=$(CURDIR)/sources/go-rwkv.cpp \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/rwkv ./backend/go/llm/rwkv
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/rwkv
endif

backend-assets/grpc/stablediffusion: sources/go-stable-diffusion sources/go-stable-diffusion/libstablediffusion.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" CPATH="$(CPATH):$(CURDIR)/sources/go-stable-diffusion/:/usr/include/opencv4" LIBRARY_PATH=$(CURDIR)/sources/go-stable-diffusion/ \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/stablediffusion ./backend/go/image/stablediffusion
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/stablediffusion
endif

backend-assets/grpc/tinydream: sources/go-tiny-dream sources/go-tiny-dream/libtinydream.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" LIBRARY_PATH=$(CURDIR)/go-tiny-dream \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/tinydream ./backend/go/image/tinydream
ifneq ($(UPX),)
	$(UPX) backend-assets/grpc/tinydream
endif

backend-assets/grpc/whisper: sources/whisper.cpp sources/whisper.cpp/libwhisper.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS) $(CGO_LDFLAGS_WHISPER)" C_INCLUDE_PATH="$(CURDIR)/sources/whisper.cpp/include:$(CURDIR)/sources/whisper.cpp/ggml/include" LIBRARY_PATH=$(CURDIR)/sources/whisper.cpp \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/whisper ./backend/go/transcribe/
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
		--build-arg BASE_IMAGE=intel/oneapi-basekit:2024.2.0-devel-ubuntu22.04 \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="none" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		--build-arg BUILD_TYPE=sycl_f32 -t $(DOCKER_IMAGE) .

docker-image-intel-xpu:
	docker build \
		--build-arg BASE_IMAGE=intel/oneapi-basekit:2024.2.0-devel-ubuntu22.04 \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="none" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		--build-arg BUILD_TYPE=sycl_f32 -t $(DOCKER_IMAGE) .

.PHONY: swagger
swagger:
	swag init -g core/http/app.go --output swagger

.PHONY: gen-assets
gen-assets:
	$(GOCMD) run core/dependencies_manager/manager.go embedded/webui_static.yaml core/http/static/assets

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
