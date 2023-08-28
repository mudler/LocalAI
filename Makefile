GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_NAME=local-ai

# llama.cpp versions
GOLLAMA_VERSION?=9072315164dc4764e10a176688b309dc848a9b25

GOLLAMA_STABLE_VERSION?=50cee7712066d9e38306eccadcfbb44ea87df4b7

# gpt4all version
GPT4ALL_REPO?=https://github.com/nomic-ai/gpt4all
GPT4ALL_VERSION?=27a8b020c36b0df8f8b82a252d261cda47cf44b8

# go-ggml-transformers version
GOGGMLTRANSFORMERS_VERSION?=ffb09d7dd71e2cbc6c5d7d05357d230eea6f369a

# go-rwkv version
RWKV_REPO?=https://github.com/donomii/go-rwkv.cpp
RWKV_VERSION?=c898cd0f62df8f2a7830e53d1d513bef4f6f792b

# whisper.cpp version
WHISPER_CPP_VERSION?=85ed71aaec8e0612a84c0b67804bde75aa75a273

# bert.cpp version
BERT_VERSION?=6abe312cded14042f6b7c3cd8edf082713334a4d

# go-piper version
PIPER_VERSION?=56b8a81b4760a6fbee1a82e62f007ae7e8f010a7

# go-bloomz version
BLOOMZ_VERSION?=1834e77b83faafe912ad4092ccf7f77937349e2f

# stablediffusion version
STABLEDIFFUSION_VERSION?=d89260f598afb809279bc72aa0107b4292587632

# Go-ggllm
GOGGLLM_VERSION?=862477d16eefb0805261c19c9b0d053e3b2b684b

export BUILD_TYPE?=
CGO_LDFLAGS?=
CUDA_LIBPATH?=/usr/local/cuda/lib64/
GO_TAGS?=
BUILD_ID?=git

VERSION?=$(shell git describe --always --tags || echo "dev" )
# go tool nm ./local-ai | grep Commit
LD_FLAGS?=
override LD_FLAGS += -X "github.com/go-skynet/LocalAI/internal.Version=$(VERSION)"
override LD_FLAGS += -X "github.com/go-skynet/LocalAI/internal.Commit=$(shell git rev-parse HEAD)"

OPTIONAL_TARGETS?=
ESPEAK_DATA?=

OS := $(shell uname -s)
ARCH := $(shell uname -m)
GREEN  := $(shell tput -Txterm setaf 2)
YELLOW := $(shell tput -Txterm setaf 3)
WHITE  := $(shell tput -Txterm setaf 7)
CYAN   := $(shell tput -Txterm setaf 6)
RESET  := $(shell tput -Txterm sgr0)

ifndef UNAME_S
UNAME_S := $(shell uname -s)
endif

# workaround for rwkv.cpp
ifeq ($(UNAME_S),Darwin)
        CGO_LDFLAGS += -lcblas -framework Accelerate 
endif

ifeq ($(BUILD_TYPE),openblas)
	CGO_LDFLAGS+=-lopenblas
endif

ifeq ($(BUILD_TYPE),cublas)
	CGO_LDFLAGS+=-lcublas -lcudart -L$(CUDA_LIBPATH)
	export LLAMA_CUBLAS=1
endif

ifeq ($(BUILD_TYPE),metal)
	CGO_LDFLAGS+=-framework Foundation -framework Metal -framework MetalKit -framework MetalPerformanceShaders
	export LLAMA_METAL=1
endif

ifeq ($(BUILD_TYPE),clblas)
	CGO_LDFLAGS+=-lOpenCL -lclblast
endif

# glibc-static or glibc-devel-static required
ifeq ($(STATIC),true)
	LD_FLAGS=-linkmode external -extldflags -static
endif

ifeq ($(findstring stablediffusion,$(GO_TAGS)),stablediffusion)
#	OPTIONAL_TARGETS+=go-stable-diffusion/libstablediffusion.a
	OPTIONAL_GRPC+=backend-assets/grpc/stablediffusion
endif

ifeq ($(findstring tts,$(GO_TAGS)),tts)
#	OPTIONAL_TARGETS+=go-piper/libpiper_binding.a
#	OPTIONAL_TARGETS+=backend-assets/espeak-ng-data
	OPTIONAL_GRPC+=backend-assets/grpc/piper
endif

GRPC_BACKENDS?=backend-assets/grpc/langchain-huggingface backend-assets/grpc/falcon-ggml backend-assets/grpc/bert-embeddings backend-assets/grpc/falcon backend-assets/grpc/bloomz backend-assets/grpc/llama backend-assets/grpc/llama-stable backend-assets/grpc/gpt4all backend-assets/grpc/dolly backend-assets/grpc/gpt2 backend-assets/grpc/gptj backend-assets/grpc/gptneox backend-assets/grpc/mpt backend-assets/grpc/replit backend-assets/grpc/starcoder backend-assets/grpc/rwkv backend-assets/grpc/whisper $(OPTIONAL_GRPC)

.PHONY: all test build vendor

all: help

## GPT4ALL
gpt4all:
	git clone --recurse-submodules $(GPT4ALL_REPO) gpt4all
	cd gpt4all && git checkout -b build $(GPT4ALL_VERSION) && git submodule update --init --recursive --depth 1

## go-ggllm
go-ggllm:
	git clone --recurse-submodules https://github.com/mudler/go-ggllm.cpp go-ggllm
	cd go-ggllm && git checkout -b build $(GOGGLLM_VERSION) && git submodule update --init --recursive --depth 1

go-ggllm/libggllm.a: go-ggllm
	$(MAKE) -C go-ggllm BUILD_TYPE=$(BUILD_TYPE) libggllm.a

## go-piper
go-piper:
	git clone --recurse-submodules https://github.com/mudler/go-piper go-piper
	cd go-piper && git checkout -b build $(PIPER_VERSION) && git submodule update --init --recursive --depth 1

## BERT embeddings
go-bert:
	git clone --recurse-submodules https://github.com/go-skynet/go-bert.cpp go-bert
	cd go-bert && git checkout -b build $(BERT_VERSION) && git submodule update --init --recursive --depth 1

## stable diffusion
go-stable-diffusion:
	git clone --recurse-submodules https://github.com/mudler/go-stable-diffusion go-stable-diffusion
	cd go-stable-diffusion && git checkout -b build $(STABLEDIFFUSION_VERSION) && git submodule update --init --recursive --depth 1

go-stable-diffusion/libstablediffusion.a:
	$(MAKE) -C go-stable-diffusion libstablediffusion.a

## RWKV
go-rwkv:
	git clone --recurse-submodules $(RWKV_REPO) go-rwkv
	cd go-rwkv && git checkout -b build $(RWKV_VERSION) && git submodule update --init --recursive --depth 1

go-rwkv/librwkv.a: go-rwkv
	cd go-rwkv && cd rwkv.cpp &&	cmake . -DRWKV_BUILD_SHARED_LIBRARY=OFF &&	cmake --build . && 	cp librwkv.a ..

## bloomz
bloomz:
	git clone --recurse-submodules https://github.com/go-skynet/bloomz.cpp bloomz
	cd bloomz && git checkout -b build $(BLOOMZ_VERSION) && git submodule update --init --recursive --depth 1

bloomz/libbloomz.a: bloomz
	cd bloomz && make libbloomz.a

go-bert/libgobert.a: go-bert
	$(MAKE) -C go-bert libgobert.a

backend-assets/gpt4all: gpt4all/gpt4all-bindings/golang/libgpt4all.a
	mkdir -p backend-assets/gpt4all
	@cp gpt4all/gpt4all-bindings/golang/buildllm/*.so backend-assets/gpt4all/ || true
	@cp gpt4all/gpt4all-bindings/golang/buildllm/*.dylib backend-assets/gpt4all/ || true
	@cp gpt4all/gpt4all-bindings/golang/buildllm/*.dll backend-assets/gpt4all/ || true

backend-assets/espeak-ng-data:
	mkdir -p backend-assets/espeak-ng-data
ifdef ESPEAK_DATA
	@cp -rf $(ESPEAK_DATA)/. backend-assets/espeak-ng-data
else
	@echo "ESPEAK_DATA not set, skipping tts. Note that this will break the tts functionality."
	@touch backend-assets/espeak-ng-data/keep
endif

gpt4all/gpt4all-bindings/golang/libgpt4all.a: gpt4all
	$(MAKE) -C gpt4all/gpt4all-bindings/golang/ libgpt4all.a

## CEREBRAS GPT
go-ggml-transformers:
	git clone --recurse-submodules https://github.com/go-skynet/go-ggml-transformers.cpp go-ggml-transformers
	cd go-ggml-transformers && git checkout -b build $(GOGPT2_VERSION) && git submodule update --init --recursive --depth 1

go-ggml-transformers/libtransformers.a: go-ggml-transformers
	$(MAKE) -C go-ggml-transformers BUILD_TYPE=$(BUILD_TYPE) libtransformers.a

whisper.cpp:
	git clone https://github.com/ggerganov/whisper.cpp.git
	cd whisper.cpp && git checkout -b build $(WHISPER_CPP_VERSION) && git submodule update --init --recursive --depth 1

whisper.cpp/libwhisper.a: whisper.cpp
	cd whisper.cpp && make libwhisper.a

go-llama:
	git clone --recurse-submodules https://github.com/go-skynet/go-llama.cpp go-llama
	cd go-llama && git checkout -b build $(GOLLAMA_VERSION) && git submodule update --init --recursive --depth 1

go-llama-stable:
	git clone --recurse-submodules https://github.com/go-skynet/go-llama.cpp go-llama-stable
	cd go-llama-stable && git checkout -b build $(GOLLAMA_STABLE_VERSION) && git submodule update --init --recursive --depth 1

go-llama/libbinding.a: go-llama
	$(MAKE) -C go-llama BUILD_TYPE=$(BUILD_TYPE) libbinding.a

go-llama-stable/libbinding.a: go-llama-stable
	$(MAKE) -C go-llama-stable BUILD_TYPE=$(BUILD_TYPE) libbinding.a

go-piper/libpiper_binding.a:
	$(MAKE) -C go-piper libpiper_binding.a example/main

get-sources: go-llama go-llama-stable go-ggllm go-ggml-transformers gpt4all go-piper go-rwkv whisper.cpp go-bert bloomz go-stable-diffusion
	touch $@

replace:
	$(GOCMD) mod edit -replace github.com/nomic-ai/gpt4all/gpt4all-bindings/golang=$(shell pwd)/gpt4all/gpt4all-bindings/golang
	$(GOCMD) mod edit -replace github.com/go-skynet/go-ggml-transformers.cpp=$(shell pwd)/go-ggml-transformers
	$(GOCMD) mod edit -replace github.com/donomii/go-rwkv.cpp=$(shell pwd)/go-rwkv
	$(GOCMD) mod edit -replace github.com/ggerganov/whisper.cpp=$(shell pwd)/whisper.cpp
	$(GOCMD) mod edit -replace github.com/go-skynet/go-bert.cpp=$(shell pwd)/go-bert
	$(GOCMD) mod edit -replace github.com/go-skynet/bloomz.cpp=$(shell pwd)/bloomz
	$(GOCMD) mod edit -replace github.com/mudler/go-stable-diffusion=$(shell pwd)/go-stable-diffusion
	$(GOCMD) mod edit -replace github.com/mudler/go-piper=$(shell pwd)/go-piper
	$(GOCMD) mod edit -replace github.com/mudler/go-ggllm.cpp=$(shell pwd)/go-ggllm

prepare-sources: get-sources replace
	$(GOCMD) mod download

## GENERIC
rebuild: ## Rebuilds the project
	$(GOCMD) clean -cache
	$(MAKE) -C go-llama clean
	$(MAKE) -C go-llama-stable clean
	$(MAKE) -C gpt4all/gpt4all-bindings/golang/ clean
	$(MAKE) -C go-ggml-transformers clean
	$(MAKE) -C go-rwkv clean
	$(MAKE) -C whisper.cpp clean
	$(MAKE) -C go-stable-diffusion clean
	$(MAKE) -C go-bert clean
	$(MAKE) -C bloomz clean
	$(MAKE) -C go-piper clean
	$(MAKE) -C go-ggllm clean
	$(MAKE) build

prepare: prepare-sources $(OPTIONAL_TARGETS) 
	touch $@

clean: ## Remove build related file
	$(GOCMD) clean -cache
	rm -f prepare
	rm -rf ./go-llama
	rm -rf ./gpt4all	
	rm -rf ./go-llama-stable
	rm -rf ./go-gpt2
	rm -rf ./go-stable-diffusion
	rm -rf ./go-ggml-transformers
	rm -rf ./backend-assets
	rm -rf ./go-rwkv
	rm -rf ./go-bert
	rm -rf ./bloomz
	rm -rf ./whisper.cpp
	rm -rf ./go-piper
	rm -rf ./go-ggllm
	rm -rf $(BINARY_NAME)
	rm -rf release/

## Build:

build: grpcs prepare ## Build the project
	$(info ${GREEN}I local-ai build info:${RESET})
	$(info ${GREEN}I BUILD_TYPE: ${YELLOW}$(BUILD_TYPE)${RESET})
	$(info ${GREEN}I GO_TAGS: ${YELLOW}$(GO_TAGS)${RESET})
	$(info ${GREEN}I LD_FLAGS: ${YELLOW}$(LD_FLAGS)${RESET})

	CGO_LDFLAGS="$(CGO_LDFLAGS)" $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o $(BINARY_NAME) ./

dist: build
	mkdir -p release
	cp $(BINARY_NAME) release/$(BINARY_NAME)-$(BUILD_ID)-$(OS)-$(ARCH)

## Run
run: prepare ## run local-ai
	CGO_LDFLAGS="$(CGO_LDFLAGS)" $(GOCMD) run ./

test-models/testmodel:
	mkdir test-models
	mkdir test-dir
	wget https://huggingface.co/nnakasato/ggml-model-test/resolve/main/ggml-model-q4.bin -O test-models/testmodel
	wget https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin -O test-models/whisper-en
	wget https://huggingface.co/skeskinen/ggml/resolve/main/all-MiniLM-L6-v2/ggml-model-q4_0.bin -O test-models/bert
	wget https://cdn.openai.com/whisper/draft-20220913a/micro-machines.wav -O test-dir/audio.wav
	wget https://huggingface.co/mudler/rwkv-4-raven-1.5B-ggml/resolve/main/RWKV-4-Raven-1B5-v11-Eng99%2525-Other1%2525-20230425-ctx4096_Q4_0.bin -O test-models/rwkv
	wget https://raw.githubusercontent.com/saharNooby/rwkv.cpp/5eb8f09c146ea8124633ab041d9ea0b1f1db4459/rwkv/20B_tokenizer.json -O test-models/rwkv.tokenizer.json
	cp tests/models_fixtures/* test-models

prepare-test: grpcs
	cp -rf backend-assets api
	cp tests/models_fixtures/* test-models

test: prepare test-models/testmodel grpcs
	@echo 'Running tests'
	export GO_TAGS="tts stablediffusion"
	$(MAKE) prepare-test
	HUGGINGFACE_GRPC=$(abspath ./)/extra/grpc/huggingface/huggingface.py TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="!gpt4all && !llama && !llama-gguf" --flake-attempts 5 -v -r ./api ./pkg
	$(MAKE) test-gpt4all
	$(MAKE) test-llama
	$(MAKE) test-llama-gguf
	$(MAKE) test-tts
	$(MAKE) test-stablediffusion

test-gpt4all: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="gpt4all" --flake-attempts 5 -v -r ./api ./pkg

test-llama: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="llama" --flake-attempts 5 -v -r ./api ./pkg

test-llama-gguf: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="llama-gguf" --flake-attempts 5 -v -r ./api ./pkg

test-tts: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="tts" --flake-attempts 1 -v -r ./api ./pkg

test-stablediffusion: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="stablediffusion" --flake-attempts 1 -v -r ./api ./pkg

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
	protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    pkg/grpc/proto/backend.proto

protogen-python:
	python3 -m grpc_tools.protoc -Ipkg/grpc/proto/ --python_out=extra/grpc/huggingface/ --grpc_python_out=extra/grpc/huggingface/ pkg/grpc/proto/backend.proto
	python3 -m grpc_tools.protoc -Ipkg/grpc/proto/ --python_out=extra/grpc/autogptq/ --grpc_python_out=extra/grpc/autogptq/ pkg/grpc/proto/backend.proto
	python3 -m grpc_tools.protoc -Ipkg/grpc/proto/ --python_out=extra/grpc/exllama/ --grpc_python_out=extra/grpc/exllama/ pkg/grpc/proto/backend.proto
	python3 -m grpc_tools.protoc -Ipkg/grpc/proto/ --python_out=extra/grpc/bark/ --grpc_python_out=extra/grpc/bark/ pkg/grpc/proto/backend.proto
	python3 -m grpc_tools.protoc -Ipkg/grpc/proto/ --python_out=extra/grpc/diffusers/ --grpc_python_out=extra/grpc/diffusers/ pkg/grpc/proto/backend.proto

## GRPC

backend-assets/grpc:
	mkdir -p backend-assets/grpc

backend-assets/grpc/falcon: backend-assets/grpc go-ggllm/libggllm.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-ggllm LIBRARY_PATH=$(shell pwd)/go-ggllm \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/falcon ./cmd/grpc/falcon/

backend-assets/grpc/llama: backend-assets/grpc go-llama/libbinding.a
	$(GOCMD) mod edit -replace github.com/go-skynet/go-llama.cpp=$(shell pwd)/go-llama
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-llama LIBRARY_PATH=$(shell pwd)/go-llama \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/llama ./cmd/grpc/llama/
# TODO: every binary should have its own folder instead, so can have different metal implementations
ifeq ($(BUILD_TYPE),metal)
	cp go-llama/build/bin/ggml-metal.metal backend-assets/grpc/
endif

backend-assets/grpc/llama-stable: backend-assets/grpc go-llama-stable/libbinding.a
	$(GOCMD) mod edit -replace github.com/go-skynet/go-llama.cpp=$(shell pwd)/go-llama-stable
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-llama-stable LIBRARY_PATH=$(shell pwd)/go-llama \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/llama-stable ./cmd/grpc/llama-stable/

backend-assets/grpc/gpt4all: backend-assets/grpc backend-assets/gpt4all gpt4all/gpt4all-bindings/golang/libgpt4all.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/gpt4all/gpt4all-bindings/golang/ LIBRARY_PATH=$(shell pwd)/gpt4all/gpt4all-bindings/golang/ \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/gpt4all ./cmd/grpc/gpt4all/

backend-assets/grpc/dolly: backend-assets/grpc go-ggml-transformers/libtransformers.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-ggml-transformers LIBRARY_PATH=$(shell pwd)/go-ggml-transformers \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/dolly ./cmd/grpc/dolly/

backend-assets/grpc/gpt2: backend-assets/grpc go-ggml-transformers/libtransformers.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-ggml-transformers LIBRARY_PATH=$(shell pwd)/go-ggml-transformers \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/gpt2 ./cmd/grpc/gpt2/

backend-assets/grpc/gptj: backend-assets/grpc go-ggml-transformers/libtransformers.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-ggml-transformers LIBRARY_PATH=$(shell pwd)/go-ggml-transformers \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/gptj ./cmd/grpc/gptj/

backend-assets/grpc/gptneox: backend-assets/grpc go-ggml-transformers/libtransformers.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-ggml-transformers LIBRARY_PATH=$(shell pwd)/go-ggml-transformers \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/gptneox ./cmd/grpc/gptneox/

backend-assets/grpc/mpt: backend-assets/grpc go-ggml-transformers/libtransformers.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-ggml-transformers LIBRARY_PATH=$(shell pwd)/go-ggml-transformers \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/mpt ./cmd/grpc/mpt/

backend-assets/grpc/replit: backend-assets/grpc go-ggml-transformers/libtransformers.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-ggml-transformers LIBRARY_PATH=$(shell pwd)/go-ggml-transformers \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/replit ./cmd/grpc/replit/

backend-assets/grpc/falcon-ggml: backend-assets/grpc go-ggml-transformers/libtransformers.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-ggml-transformers LIBRARY_PATH=$(shell pwd)/go-ggml-transformers \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/falcon-ggml ./cmd/grpc/falcon-ggml/

backend-assets/grpc/starcoder: backend-assets/grpc go-ggml-transformers/libtransformers.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-ggml-transformers LIBRARY_PATH=$(shell pwd)/go-ggml-transformers \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/starcoder ./cmd/grpc/starcoder/

backend-assets/grpc/rwkv: backend-assets/grpc go-rwkv/librwkv.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-rwkv LIBRARY_PATH=$(shell pwd)/go-rwkv \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/rwkv ./cmd/grpc/rwkv/

backend-assets/grpc/bloomz: backend-assets/grpc bloomz/libbloomz.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/bloomz LIBRARY_PATH=$(shell pwd)/bloomz \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/bloomz ./cmd/grpc/bloomz/

backend-assets/grpc/bert-embeddings: backend-assets/grpc go-bert/libgobert.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-bert LIBRARY_PATH=$(shell pwd)/go-bert \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/bert-embeddings ./cmd/grpc/bert-embeddings/

backend-assets/grpc/langchain-huggingface: backend-assets/grpc
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/langchain-huggingface ./cmd/grpc/langchain-huggingface/

backend-assets/grpc/stablediffusion: backend-assets/grpc go-stable-diffusion/libstablediffusion.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/go-stable-diffusion/ LIBRARY_PATH=$(shell pwd)/go-stable-diffusion/ \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/stablediffusion ./cmd/grpc/stablediffusion/

backend-assets/grpc/piper: backend-assets/grpc backend-assets/espeak-ng-data go-piper/libpiper_binding.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" LIBRARY_PATH=$(shell pwd)/go-piper \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/piper ./cmd/grpc/piper/

backend-assets/grpc/whisper: backend-assets/grpc whisper.cpp/libwhisper.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=$(shell pwd)/whisper.cpp LIBRARY_PATH=$(shell pwd)/whisper.cpp \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o backend-assets/grpc/whisper ./cmd/grpc/whisper/

grpcs: prepare $(GRPC_BACKENDS)
