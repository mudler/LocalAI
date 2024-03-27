GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_NAME=local-ai

# llama.cpp versions
GOLLAMA_STABLE_VERSION?=2b57a8ae43e4699d3dc5d1496a1ccd42922993be
CPPLLAMA_VERSION?=b06c16ef9f81d84da520232c125d4d8a1d273736

# gpt4all version
GPT4ALL_REPO?=https://github.com/nomic-ai/gpt4all
GPT4ALL_VERSION?=27a8b020c36b0df8f8b82a252d261cda47cf44b8

# go-rwkv version
RWKV_REPO?=https://github.com/donomii/go-rwkv.cpp
RWKV_VERSION?=661e7ae26d442f5cfebd2a0881b44e8c55949ec6

# whisper.cpp version
WHISPER_CPP_VERSION?=1558ec5a16cb2b2a0bf54815df1d41f83dc3815b

# bert.cpp version
BERT_VERSION?=6abe312cded14042f6b7c3cd8edf082713334a4d

# go-piper version
PIPER_VERSION?=9d0100873a7dbb0824dfea40e8cec70a1b110759

# stablediffusion version
STABLEDIFFUSION_VERSION?=362df9da29f882dbf09ade61972d16a1f53c3485

# tinydream version
TINYDREAM_VERSION?=772a9c0d9aaf768290e63cca3c904fe69faf677a

export BUILD_TYPE?=
export STABLE_BUILD_TYPE?=$(BUILD_TYPE)
export CMAKE_ARGS?=

CGO_LDFLAGS?=
CGO_LDFLAGS_WHISPER?=
CUDA_LIBPATH?=/usr/local/cuda/lib64/
GO_TAGS?=
BUILD_ID?=git

TEST_DIR=/tmp/test

TEST_FLAKES?=5

RANDOM := $(shell bash -c 'echo $$RANDOM')

VERSION?=$(shell git describe --always --tags || echo "dev" )
# go tool nm ./local-ai | grep Commit
LD_FLAGS?=
override LD_FLAGS += -X "github.com/go-skynet/LocalAI/internal.Version=$(VERSION)"
override LD_FLAGS += -X "github.com/go-skynet/LocalAI/internal.Commit=$(shell git rev-parse HEAD)"

OPTIONAL_TARGETS?=

OS := $(shell uname -s)
ARCH := $(shell uname -m)
GREEN  := $(shell tput -Txterm setaf 2)
YELLOW := $(shell tput -Txterm setaf 3)
WHITE  := $(shell tput -Txterm setaf 7)
CYAN   := $(shell tput -Txterm setaf 6)
RESET  := $(shell tput -Txterm sgr0)

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
		CMAKE_ARGS+=-DLLAMA_METAL=OFF
		export LLAMA_NO_ACCELERATE=1
	endif

	ifeq ($(BUILD_TYPE),metal)
#			-lcblas 	removed: it seems to always be listed as a duplicate flag.
		CGO_LDFLAGS += -framework Accelerate
	endif
endif

ifeq ($(BUILD_TYPE),openblas)
	CGO_LDFLAGS+=-lopenblas
	export WHISPER_OPENBLAS=1
endif


ifeq ($(BUILD_TYPE),cublas)
	CGO_LDFLAGS+=-lcublas -lcudart -L$(CUDA_LIBPATH)
	export LLAMA_CUBLAS=1
	export WHISPER_CUBLAS=1
	CGO_LDFLAGS_WHISPER+=-L$(CUDA_LIBPATH)/stubs/ -lcuda
endif

ifeq ($(BUILD_TYPE),hipblas)
	ROCM_HOME ?= /opt/rocm
	ROCM_PATH ?= /opt/rocm
	LD_LIBRARY_PATH ?= /opt/rocm/lib:/opt/rocm/llvm/lib
	export CXX=$(ROCM_HOME)/llvm/bin/clang++
	export CC=$(ROCM_HOME)/llvm/bin/clang
	# llama-ggml has no hipblas support, so override it here.
	export STABLE_BUILD_TYPE=
	export WHISPER_HIPBLAS=1
	GPU_TARGETS ?= gfx900,gfx90a,gfx1030,gfx1031,gfx1100
	AMDGPU_TARGETS ?= "$(GPU_TARGETS)"
	CMAKE_ARGS+=-DLLAMA_HIPBLAS=ON -DAMDGPU_TARGETS="$(AMDGPU_TARGETS)" -DGPU_TARGETS="$(GPU_TARGETS)"
	CGO_LDFLAGS += -O3 --rtlib=compiler-rt -unwindlib=libgcc -lhipblas -lrocblas --hip-link -L${ROCM_HOME}/lib/llvm/lib
endif

ifeq ($(BUILD_TYPE),metal)
	CGO_LDFLAGS+=-framework Foundation -framework Metal -framework MetalKit -framework MetalPerformanceShaders
	export LLAMA_METAL=1
	export WHISPER_METAL=1
endif

ifeq ($(BUILD_TYPE),clblas)
	CGO_LDFLAGS+=-lOpenCL -lclblast
	export WHISPER_CLBLAST=1
endif

# glibc-static or glibc-devel-static required
ifeq ($(STATIC),true)
	LD_FLAGS=-linkmode external -extldflags -static
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

ALL_GRPC_BACKENDS=backend-assets/grpc/langchain-huggingface
ALL_GRPC_BACKENDS+=backend-assets/grpc/bert-embeddings
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-cpp
ALL_GRPC_BACKENDS+=backend-assets/grpc/llama-ggml
ALL_GRPC_BACKENDS+=backend-assets/grpc/gpt4all
ALL_GRPC_BACKENDS+=backend-assets/grpc/rwkv
ALL_GRPC_BACKENDS+=backend-assets/grpc/whisper
ALL_GRPC_BACKENDS+=backend-assets/grpc/local-store
ALL_GRPC_BACKENDS+=$(OPTIONAL_GRPC)

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
sources/go-bert:
	git clone --recurse-submodules https://github.com/go-skynet/go-bert.cpp sources/go-bert
	cd sources/go-bert && git checkout -b build $(BERT_VERSION) && git submodule update --init --recursive --depth 1

sources/go-bert/libgobert.a: sources/go-bert
	$(MAKE) -C sources/go-bert libgobert.a

## go-llama-ggml
sources/go-llama-ggml:
	git clone --recurse-submodules https://github.com/go-skynet/go-llama.cpp sources/go-llama-ggml
	cd sources/go-llama-ggml && git checkout -b build $(GOLLAMA_STABLE_VERSION) && git submodule update --init --recursive --depth 1

sources/go-llama-ggml/libbinding.a: sources/go-llama-ggml
	$(MAKE) -C sources/go-llama-ggml BUILD_TYPE=$(STABLE_BUILD_TYPE) libbinding.a

## go-piper
sources/go-piper:
	git clone --recurse-submodules https://github.com/mudler/go-piper sources/go-piper
	cd sources/go-piper && git checkout -b build $(PIPER_VERSION) && git submodule update --init --recursive --depth 1

sources/go-piper/libpiper_binding.a: sources/go-piper
	$(MAKE) -C sources/go-piper libpiper_binding.a example/main piper.o

## GPT4ALL
sources/gpt4all:
	git clone --recurse-submodules $(GPT4ALL_REPO) sources/gpt4all
	cd sources/gpt4all && git checkout -b build $(GPT4ALL_VERSION) && git submodule update --init --recursive --depth 1

sources/gpt4all/gpt4all-bindings/golang/libgpt4all.a: sources/gpt4all
	$(MAKE) -C sources/gpt4all/gpt4all-bindings/golang/ libgpt4all.a

## RWKV
sources/go-rwkv:
	git clone --recurse-submodules $(RWKV_REPO) sources/go-rwkv
	cd sources/go-rwkv && git checkout -b build $(RWKV_VERSION) && git submodule update --init --recursive --depth 1

sources/go-rwkv/librwkv.a: sources/go-rwkv
	cd sources/go-rwkv && cd rwkv.cpp &&	cmake . -DRWKV_BUILD_SHARED_LIBRARY=OFF &&	cmake --build . && 	cp librwkv.a ..

## stable diffusion
sources/go-stable-diffusion:
	git clone --recurse-submodules https://github.com/mudler/go-stable-diffusion sources/go-stable-diffusion
	cd sources/go-stable-diffusion && git checkout -b build $(STABLEDIFFUSION_VERSION) && git submodule update --init --recursive --depth 1

sources/go-stable-diffusion/libstablediffusion.a: sources/go-stable-diffusion
	$(MAKE) -C sources/go-stable-diffusion libstablediffusion.a

## tiny-dream
sources/go-tiny-dream:
	git clone --recurse-submodules https://github.com/M0Rf30/go-tiny-dream sources/go-tiny-dream
	cd sources/go-tiny-dream && git checkout -b build $(TINYDREAM_VERSION) && git submodule update --init --recursive --depth 1

sources/go-tiny-dream/libtinydream.a: sources/go-tiny-dream
	$(MAKE) -C sources/go-tiny-dream libtinydream.a

## whisper
sources/whisper.cpp:
	git clone https://github.com/ggerganov/whisper.cpp.git sources/whisper.cpp
	cd sources/whisper.cpp && git checkout -b build $(WHISPER_CPP_VERSION) && git submodule update --init --recursive --depth 1

sources/whisper.cpp/libwhisper.a: sources/whisper.cpp
	cd sources/whisper.cpp && make libwhisper.a

get-sources: sources/go-llama-ggml sources/gpt4all sources/go-piper sources/go-rwkv sources/whisper.cpp sources/go-bert sources/go-stable-diffusion sources/go-tiny-dream

replace:
	$(GOCMD) mod edit -replace github.com/donomii/go-rwkv.cpp=$(CURDIR)/sources/go-rwkv
	$(GOCMD) mod edit -replace github.com/ggerganov/whisper.cpp=$(CURDIR)/sources/whisper.cpp
	$(GOCMD) mod edit -replace github.com/ggerganov/whisper.cpp/bindings/go=$(CURDIR)/sources/whisper.cpp/bindings/go
	$(GOCMD) mod edit -replace github.com/go-skynet/go-bert.cpp=$(CURDIR)/sources/go-bert
	$(GOCMD) mod edit -replace github.com/M0Rf30/go-tiny-dream=$(CURDIR)/sources/go-tiny-dream
	$(GOCMD) mod edit -replace github.com/mudler/go-piper=$(CURDIR)/sources/go-piper
	$(GOCMD) mod edit -replace github.com/mudler/go-stable-diffusion=$(CURDIR)/sources/go-stable-diffusion
	$(GOCMD) mod edit -replace github.com/nomic-ai/gpt4all/gpt4all-bindings/golang=$(CURDIR)/sources/gpt4all/gpt4all-bindings/golang

dropreplace:
	$(GOCMD) mod edit -dropreplace github.com/donomii/go-rwkv.cpp
	$(GOCMD) mod edit -dropreplace github.com/ggerganov/whisper.cpp
	$(GOCMD) mod edit -dropreplace github.com/ggerganov/whisper.cpp/bindings/go
	$(GOCMD) mod edit -dropreplace github.com/go-skynet/go-bert.cpp
	$(GOCMD) mod edit -dropreplace github.com/M0Rf30/go-tiny-dream
	$(GOCMD) mod edit -dropreplace github.com/mudler/go-piper
	$(GOCMD) mod edit -dropreplace github.com/mudler/go-stable-diffusion
	$(GOCMD) mod edit -dropreplace github.com/nomic-ai/gpt4all/gpt4all-bindings/golang

prepare-sources: get-sources replace
	$(GOCMD) mod download

## GENERIC
rebuild: ## Rebuilds the project
	$(GOCMD) clean -cache
	$(MAKE) -C sources/go-llama-ggml clean
	$(MAKE) -C sources/gpt4all/gpt4all-bindings/golang/ clean
	$(MAKE) -C sources/go-rwkv clean
	$(MAKE) -C sources/whisper.cpp clean
	$(MAKE) -C sources/go-stable-diffusion clean
	$(MAKE) -C sources/go-bert clean
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
	rm -rf backend-assets
	$(MAKE) -C backend/cpp/grpc clean
	$(MAKE) -C backend/cpp/llama clean
	$(MAKE) dropreplace

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
	CGO_LDFLAGS="$(CGO_LDFLAGS)" $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o $(BINARY_NAME) ./

dist: build
	mkdir -p release
	cp $(BINARY_NAME) release/$(BINARY_NAME)-$(BUILD_ID)-$(OS)-$(ARCH)

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
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="!gpt4all && !llama && !llama-gguf"  --flake-attempts $(TEST_FLAKES) --fail-fast -v -r $(TEST_PATHS)
	$(MAKE) test-gpt4all
	$(MAKE) test-llama
	$(MAKE) test-llama-gguf
	$(MAKE) test-tts
	$(MAKE) test-stablediffusion

prepare-e2e:
	mkdir -p $(TEST_DIR)
	cp -rfv $(abspath ./tests/e2e-fixtures)/gpu.yaml $(TEST_DIR)/gpu.yaml
	test -e $(TEST_DIR)/ggllm-test-model.bin || wget -q https://huggingface.co/TheBloke/CodeLlama-7B-Instruct-GGUF/resolve/main/codellama-7b-instruct.Q2_K.gguf -O $(TEST_DIR)/ggllm-test-model.bin
	docker build --build-arg BUILD_GRPC=true --build-arg GRPC_BACKENDS="$(GRPC_BACKENDS)" --build-arg IMAGE_TYPE=core --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg CUDA_MAJOR_VERSION=11 --build-arg CUDA_MINOR_VERSION=7 --build-arg FFMPEG=true -t localai-tests .

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

test-gpt4all: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="gpt4all" --flake-attempts 5 -v -r $(TEST_PATHS)

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

protogen: protogen-go protogen-python

protogen-go:
	protoc -Ibackend/ --go_out=pkg/grpc/proto/ --go_opt=paths=source_relative --go-grpc_out=pkg/grpc/proto/ --go-grpc_opt=paths=source_relative \
    backend/backend.proto

protogen-python:
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/sentencetransformers/ --grpc_python_out=backend/python/sentencetransformers/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/transformers/ --grpc_python_out=backend/python/transformers/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/transformers-musicgen/ --grpc_python_out=backend/python/transformers-musicgen/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/autogptq/ --grpc_python_out=backend/python/autogptq/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/exllama/ --grpc_python_out=backend/python/exllama/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/bark/ --grpc_python_out=backend/python/bark/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/diffusers/ --grpc_python_out=backend/python/diffusers/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/coqui/ --grpc_python_out=backend/python/coqui/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/vall-e-x/ --grpc_python_out=backend/python/vall-e-x/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/vllm/ --grpc_python_out=backend/python/vllm/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/petals/ --grpc_python_out=backend/python/petals/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/mamba/ --grpc_python_out=backend/python/mamba/ backend/backend.proto
	python3 -m grpc_tools.protoc -Ibackend/ --python_out=backend/python/exllama2/ --grpc_python_out=backend/python/exllama2/ backend/backend.proto

## GRPC
# Note: it is duplicated in the Dockerfile
prepare-extra-conda-environments:
	$(MAKE) -C backend/python/autogptq
	$(MAKE) -C backend/python/bark
	$(MAKE) -C backend/python/coqui
	$(MAKE) -C backend/python/diffusers
	$(MAKE) -C backend/python/vllm
	$(MAKE) -C backend/python/mamba
	$(MAKE) -C backend/python/sentencetransformers
	$(MAKE) -C backend/python/transformers
	$(MAKE) -C backend/python/transformers-musicgen
	$(MAKE) -C backend/python/vall-e-x
	$(MAKE) -C backend/python/exllama
	$(MAKE) -C backend/python/petals
	$(MAKE) -C backend/python/exllama2

prepare-test-extra:
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

backend-assets/gpt4all: sources/gpt4all sources/gpt4all/gpt4all-bindings/golang/libgpt4all.a
	mkdir -p backend-assets/gpt4all
	@cp sources/gpt4all/gpt4all-bindings/golang/buildllm/*.so backend-assets/gpt4all/ || true
	@cp sources/gpt4all/gpt4all-bindings/golang/buildllm/*.dylib backend-assets/gpt4all/ || true
	@cp sources/gpt4all/gpt4all-bindings/golang/buildllm/*.dll backend-assets/gpt4all/ || true

backend-assets/grpc: replace
	mkdir -p backend-assets/grpc

backend-assets/grpc/bert-embeddings: sources/go-bert sources/go-bert/libgobert.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(CURDIR)/sources/go-bert LIBRARY_PATH=$(CURDIR)/sources/go-bert \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/bert-embeddings ./backend/go/llm/bert/

backend-assets/grpc/gpt4all: sources/gpt4all sources/gpt4all/gpt4all-bindings/golang/libgpt4all.a backend-assets/gpt4all backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(CURDIR)/sources/gpt4all/gpt4all-bindings/golang/ LIBRARY_PATH=$(CURDIR)/sources/gpt4all/gpt4all-bindings/golang/ \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/gpt4all ./backend/go/llm/gpt4all/

backend-assets/grpc/langchain-huggingface: backend-assets/grpc
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/langchain-huggingface ./backend/go/llm/langchain/

backend/cpp/llama/llama.cpp:
	LLAMA_VERSION=$(CPPLLAMA_VERSION) $(MAKE) -C backend/cpp/llama llama.cpp

INSTALLED_PACKAGES=$(CURDIR)/backend/cpp/grpc/installed_packages
INSTALLED_LIB_CMAKE=$(INSTALLED_PACKAGES)/lib/cmake
ADDED_CMAKE_ARGS=-Dabsl_DIR=${INSTALLED_LIB_CMAKE}/absl \
				 -DProtobuf_DIR=${INSTALLED_LIB_CMAKE}/protobuf \
				 -Dutf8_range_DIR=${INSTALLED_LIB_CMAKE}/utf8_range \
				 -DgRPC_DIR=${INSTALLED_LIB_CMAKE}/grpc \
				 -DCMAKE_CXX_STANDARD_INCLUDE_DIRECTORIES=${INSTALLED_PACKAGES}/include
backend/cpp/llama/grpc-server:
# Conditionally build grpc for the llama backend to use if needed
ifdef BUILD_GRPC_FOR_BACKEND_LLAMA
	$(MAKE) -C backend/cpp/grpc build
	_PROTOBUF_PROTOC=${INSTALLED_PACKAGES}/bin/proto \
	_GRPC_CPP_PLUGIN_EXECUTABLE=${INSTALLED_PACKAGES}/bin/grpc_cpp_plugin \
	PATH="${INSTALLED_PACKAGES}/bin:${PATH}" \
	CMAKE_ARGS="${CMAKE_ARGS} ${ADDED_CMAKE_ARGS}" \
	LLAMA_VERSION=$(CPPLLAMA_VERSION) \
	$(MAKE) -C backend/cpp/llama grpc-server
else
	echo "BUILD_GRPC_FOR_BACKEND_LLAMA is not defined."
	LLAMA_VERSION=$(CPPLLAMA_VERSION) $(MAKE) -C backend/cpp/llama grpc-server
endif

backend-assets/grpc/llama-cpp: backend-assets/grpc backend/cpp/llama/grpc-server
	cp -rfv backend/cpp/llama/grpc-server backend-assets/grpc/llama-cpp
# TODO: every binary should have its own folder instead, so can have different metal implementations
ifeq ($(BUILD_TYPE),metal)
	cp backend/cpp/llama/llama.cpp/build/bin/default.metallib backend-assets/grpc/
endif

backend-assets/grpc/llama-ggml: sources/go-llama-ggml sources/go-llama-ggml/libbinding.a backend-assets/grpc
	$(GOCMD) mod edit -replace github.com/go-skynet/go-llama.cpp=$(CURDIR)/sources/go-llama-ggml
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(CURDIR)/sources/go-llama-ggml LIBRARY_PATH=$(CURDIR)/sources/go-llama-ggml \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/llama-ggml ./backend/go/llm/llama-ggml/

backend-assets/grpc/piper: sources/go-piper sources/go-piper/libpiper_binding.a backend-assets/grpc backend-assets/espeak-ng-data
	CGO_CXXFLAGS="$(PIPER_CGO_CXXFLAGS)" CGO_LDFLAGS="$(PIPER_CGO_LDFLAGS)" LIBRARY_PATH=$(CURDIR)/sources/go-piper \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/piper ./backend/go/tts/

backend-assets/grpc/rwkv: sources/go-rwkv sources/go-rwkv/librwkv.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(CURDIR)/sources/go-rwkv LIBRARY_PATH=$(CURDIR)/sources/go-rwkv \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/rwkv ./backend/go/llm/rwkv

backend-assets/grpc/stablediffusion: sources/go-stable-diffusion sources/go-stable-diffusion/libstablediffusion.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(CURDIR)/sources/go-stable-diffusion/ LIBRARY_PATH=$(CURDIR)/sources/go-stable-diffusion/ \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/stablediffusion ./backend/go/image/stablediffusion

backend-assets/grpc/tinydream: sources/go-tiny-dream sources/go-tiny-dream/libtinydream.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS)" LIBRARY_PATH=$(CURDIR)/go-tiny-dream \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/tinydream ./backend/go/image/tinydream

backend-assets/grpc/whisper: sources/whisper.cpp sources/whisper.cpp/libwhisper.a backend-assets/grpc
	CGO_LDFLAGS="$(CGO_LDFLAGS) $(CGO_LDFLAGS_WHISPER)" C_INCLUDE_PATH=$(CURDIR)/sources/whisper.cpp LIBRARY_PATH=$(CURDIR)/sources/whisper.cpp \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/whisper ./backend/go/transcribe/

backend-assets/grpc/local-store: backend-assets/grpc
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/local-store ./backend/go/stores/

grpcs: prepare $(GRPC_BACKENDS)

DOCKER_IMAGE?=local-ai
DOCKER_AIO_IMAGE?=local-ai-aio
IMAGE_TYPE?=core
BASE_IMAGE?=ubuntu:22.04

docker:
	docker build \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS=$(GO_TAGS) \
		--build-arg BUILD_TYPE=$(BUILD_TYPE) \
		-t $(DOCKER_IMAGE) .
	
docker-aio:
	@echo "Building AIO image with base $(BASE_IMAGE) as $(DOCKER_AIO_IMAGE)"
	docker build \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		-t $(DOCKER_AIO_IMAGE) -f Dockerfile.aio .

docker-aio-all:
	$(MAKE) docker-aio DOCKER_AIO_SIZE=cpu
	$(MAKE) docker-aio DOCKER_AIO_SIZE=cpu

docker-image-intel:
	docker build \
		--build-arg BASE_IMAGE=intel/oneapi-basekit:2024.0.1-devel-ubuntu22.04 \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="none" \
		--build-arg BUILD_TYPE=sycl_f32 -t $(DOCKER_IMAGE) .

docker-image-intel-xpu:
	docker build \
		--build-arg BASE_IMAGE=intel/oneapi-basekit:2024.0.1-devel-ubuntu22.04 \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="none" \
		--build-arg BUILD_TYPE=sycl_f32 -t $(DOCKER_IMAGE) .
