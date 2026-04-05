# Disable parallel execution for backend builds
.NOTPARALLEL: backends/diffusers backends/llama-cpp backends/outetts backends/piper backends/stablediffusion-ggml backends/whisper backends/faster-whisper backends/silero-vad backends/local-store backends/huggingface backends/avian backends/rfdetr backends/kitten-tts backends/kokoro backends/chatterbox backends/llama-cpp-darwin backends/neutts build-darwin-python-backend build-darwin-go-backend backends/mlx backends/diffuser-darwin backends/mlx-vlm backends/mlx-audio backends/mlx-distributed backends/stablediffusion-ggml-darwin backends/vllm backends/vllm-omni backends/moonshine backends/pocket-tts backends/qwen-tts backends/faster-qwen3-tts backends/qwen-asr backends/nemo backends/voxcpm backends/whisperx backends/ace-step backends/acestep-cpp backends/fish-speech backends/voxtral backends/opus backends/trl backends/llama-cpp-quantization

GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_NAME=local-ai
LAUNCHER_BINARY_NAME=local-ai-launcher

UBUNTU_VERSION?=2404
UBUNTU_CODENAME?=noble

GORELEASER?=

export BUILD_TYPE?=
export CUDA_MAJOR_VERSION?=13
export CUDA_MINOR_VERSION?=0

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

# Default Docker bridge IP
E2E_BRIDGE_IP?=172.17.0.1

ifndef UNAME_S
UNAME_S := $(shell uname -s)
endif

ifeq ($(OS),Darwin)
	ifeq ($(OSX_SIGNING_IDENTITY),)
		OSX_SIGNING_IDENTITY := $(shell security find-identity -v -p codesigning | grep '"' | head -n 1 | sed -E 's/.*"(.*)"/\1/')
	endif
endif

# check if goreleaser exists
ifeq (, $(shell which goreleaser))
	GORELEASER=curl -sfL https://goreleaser.com/static/run | bash -s --
else
	GORELEASER=$(shell which goreleaser)
endif

TEST_PATHS?=./api/... ./pkg/... ./core/...


.PHONY: all test build vendor

all: help

## GENERIC
rebuild: ## Rebuilds the project
	$(GOCMD) clean -cache
	$(MAKE) build

clean: ## Remove build related file
	$(GOCMD) clean -cache
	rm -f prepare
	rm -rf $(BINARY_NAME)
	rm -rf release/
	$(MAKE) protogen-clean
	rmdir pkg/grpc/proto || true

clean-tests:
	rm -rf test-models
	rm -rf test-dir

## Install Go tools
install-go-tools:
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2

## React UI:
react-ui:
ifneq ($(wildcard core/http/react-ui/dist),)
	@echo "react-ui dist already exists, skipping build"
else
	cd core/http/react-ui && npm install && npm run build
endif

react-ui-docker:
	docker run --entrypoint /bin/bash -v $(CURDIR):/app:z oven/bun:1 \
	  -c "cd /app/core/http/react-ui && bun install && bun run build"

core/http/react-ui/dist: react-ui

## Build:

build: protogen-go generate install-go-tools core/http/react-ui/dist ## Build the project
	$(info ${GREEN}I local-ai build info:${RESET})
	$(info ${GREEN}I BUILD_TYPE: ${YELLOW}$(BUILD_TYPE)${RESET})
	$(info ${GREEN}I GO_TAGS: ${YELLOW}$(GO_TAGS)${RESET})
	$(info ${GREEN}I LD_FLAGS: ${YELLOW}$(LD_FLAGS)${RESET})
	$(info ${GREEN}I UPX: ${YELLOW}$(UPX)${RESET})
	rm -rf $(BINARY_NAME) || true
	CGO_LDFLAGS="$(CGO_LDFLAGS)" $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o $(BINARY_NAME) ./cmd/local-ai

build-launcher: ## Build the launcher application
	$(info ${GREEN}I local-ai launcher build info:${RESET})
	$(info ${GREEN}I BUILD_TYPE: ${YELLOW}$(BUILD_TYPE)${RESET})
	$(info ${GREEN}I GO_TAGS: ${YELLOW}$(GO_TAGS)${RESET})
	$(info ${GREEN}I LD_FLAGS: ${YELLOW}$(LD_FLAGS)${RESET})
	rm -rf $(LAUNCHER_BINARY_NAME) || true
	CGO_LDFLAGS="$(CGO_LDFLAGS)" $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o $(LAUNCHER_BINARY_NAME) ./cmd/launcher

build-all: build build-launcher ## Build both server and launcher

build-dev: ## Run LocalAI in dev mode with live reload
	@command -v air >/dev/null 2>&1 || go install github.com/air-verse/air@latest
	air -c .air.toml

dev-dist:
	$(GORELEASER) build --snapshot --clean

dist:
	$(GORELEASER) build --clean

osx-signed: build
	codesign --deep --force --sign "$(OSX_SIGNING_IDENTITY)" --entitlements "./Entitlements.plist" "./$(BINARY_NAME)"

## Run
run: ## run local-ai
	CGO_LDFLAGS="$(CGO_LDFLAGS)" $(GOCMD) run ./

test-models/testmodel.ggml:
	mkdir -p test-models
	mkdir -p test-dir
	wget -q https://huggingface.co/mradermacher/gpt2-alpaca-gpt4-GGUF/resolve/main/gpt2-alpaca-gpt4.Q4_K_M.gguf -O test-models/testmodel.ggml
	wget -q https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin -O test-models/whisper-en
	wget -q https://huggingface.co/mudler/all-MiniLM-L6-v2/resolve/main/ggml-model-q4_0.bin -O test-models/bert
	wget -q https://cdn.openai.com/whisper/draft-20220913a/micro-machines.wav -O test-dir/audio.wav
	cp tests/models_fixtures/* test-models

prepare-test: protogen-go
	cp tests/models_fixtures/* test-models

########################################################
## Tests
########################################################

## Test targets
test: test-models/testmodel.ggml protogen-go
	@echo 'Running tests'
	export GO_TAGS="debug"
	$(MAKE) prepare-test
	OPUS_SHIM_LIBRARY=$(abspath ./pkg/opus/shim/libopusshim.so) \
	HUGGINGFACE_GRPC=$(abspath ./)/backend/python/transformers/run.sh TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models BACKENDS_PATH=$(abspath ./)/backends \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="!llama-gguf"  --flake-attempts $(TEST_FLAKES) --fail-fast -v -r $(TEST_PATHS)
	$(MAKE) test-llama-gguf
	$(MAKE) test-tts
	$(MAKE) test-stablediffusion

########################################################
## E2E AIO tests (uses standard image with pre-configured models)
########################################################

docker-build-e2e:
	docker build \
		--build-arg MAKEFLAGS="--jobs=5 --output-sync=target" \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg BUILD_TYPE=$(BUILD_TYPE) \
		--build-arg CUDA_MAJOR_VERSION=$(CUDA_MAJOR_VERSION) \
		--build-arg CUDA_MINOR_VERSION=$(CUDA_MINOR_VERSION) \
		--build-arg UBUNTU_VERSION=$(UBUNTU_VERSION) \
		--build-arg UBUNTU_CODENAME=$(UBUNTU_CODENAME) \
		--build-arg GO_TAGS="$(GO_TAGS)" \
		-t local-ai:tests -f Dockerfile .

e2e-aio:
	LOCALAI_BACKEND_DIR=$(abspath ./backends) \
	LOCALAI_MODELS_DIR=$(abspath ./tests/e2e-aio/models) \
	LOCALAI_IMAGE_TAG=tests \
	LOCALAI_IMAGE=local-ai \
	$(MAKE) run-e2e-aio

run-e2e-aio: protogen-go
	@echo 'Running e2e AIO tests'
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r ./tests/e2e-aio

########################################################
## E2E tests
########################################################

prepare-e2e:
	docker build \
		--build-arg IMAGE_TYPE=core \
		--build-arg BUILD_TYPE=$(BUILD_TYPE) \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		--build-arg CUDA_MAJOR_VERSION=$(CUDA_MAJOR_VERSION) \
		--build-arg CUDA_MINOR_VERSION=$(CUDA_MINOR_VERSION) \
		--build-arg UBUNTU_VERSION=$(UBUNTU_VERSION) \
		--build-arg UBUNTU_CODENAME=$(UBUNTU_CODENAME) \
		--build-arg GO_TAGS="$(GO_TAGS)" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		-t localai-tests .

run-e2e-image:
	docker run -p 5390:8080 -e MODELS_PATH=/models -e THREADS=1 -e DEBUG=true -d --rm -v $(TEST_DIR):/models --name e2e-tests-$(RANDOM) localai-tests

test-e2e: build-mock-backend prepare-e2e run-e2e-image
	@echo 'Running e2e tests'
	BUILD_TYPE=$(BUILD_TYPE) \
	LOCALAI_API=http://$(E2E_BRIDGE_IP):5390 \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r ./tests/e2e
	$(MAKE) clean-mock-backend
	$(MAKE) teardown-e2e
	docker rmi localai-tests

teardown-e2e:
	rm -rf $(TEST_DIR) || true
	docker stop $$(docker ps -q --filter ancestor=localai-tests)

########################################################
## Integration and unit tests
########################################################

test-llama-gguf: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models BACKENDS_PATH=$(abspath ./)/backends \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="llama-gguf" --flake-attempts $(TEST_FLAKES) -v -r $(TEST_PATHS)

test-tts: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models BACKENDS_PATH=$(abspath ./)/backends \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="tts" --flake-attempts $(TEST_FLAKES) -v -r $(TEST_PATHS)

test-stablediffusion: prepare-test
	TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models BACKENDS_PATH=$(abspath ./)/backends \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="stablediffusion" --flake-attempts $(TEST_FLAKES) -v -r $(TEST_PATHS)

test-stores:
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="stores" --flake-attempts $(TEST_FLAKES) -v -r tests/integration

test-opus:
	@echo 'Running opus backend tests'
	$(MAKE) -C backend/go/opus libopusshim.so
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r ./backend/go/opus/...

test-opus-docker:
	@echo 'Running opus backend tests in Docker'
	docker build --target builder \
	  --build-arg BUILD_TYPE=$(or $(BUILD_TYPE),) \
	  --build-arg BASE_IMAGE=$(or $(BASE_IMAGE),ubuntu:24.04) \
	  --build-arg BACKEND=opus \
	  -t localai-opus-test -f backend/Dockerfile.golang .
	docker run --rm localai-opus-test \
	  bash -c 'cd /LocalAI && go run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r ./backend/go/opus/...'

test-realtime: build-mock-backend
	@echo 'Running realtime e2e tests (mock backend)'
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="Realtime && !real-models" --flake-attempts $(TEST_FLAKES) -v -r ./tests/e2e

# Real-model realtime tests. Set REALTIME_TEST_MODEL to use your own pipeline,
# or leave unset to auto-build one from the component env vars below.
REALTIME_VAD?=silero-vad-ggml
REALTIME_STT?=whisper-1
REALTIME_LLM?=qwen3-0.6b
REALTIME_TTS?=tts-1
REALTIME_BACKENDS_PATH?=$(abspath ./)/backends

test-realtime-models: build-mock-backend
	@echo 'Running realtime e2e tests (real models)'
	REALTIME_TEST_MODEL=$${REALTIME_TEST_MODEL:-realtime-test-pipeline} \
	REALTIME_VAD=$(REALTIME_VAD) \
	REALTIME_STT=$(REALTIME_STT) \
	REALTIME_LLM=$(REALTIME_LLM) \
	REALTIME_TTS=$(REALTIME_TTS) \
	REALTIME_BACKENDS_PATH=$(REALTIME_BACKENDS_PATH) \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="Realtime" --flake-attempts $(TEST_FLAKES) -v -r ./tests/e2e

# --- Container-based real-model testing ---

REALTIME_BACKEND_NAMES ?= silero-vad whisper llama-cpp kokoro
REALTIME_MODELS_DIR ?= $(abspath ./models)
REALTIME_BACKENDS_DIR ?= $(abspath ./local-backends)
REALTIME_DOCKER_FLAGS ?= --gpus all

local-backends:
	mkdir -p local-backends

extract-backend-%: docker-build-% local-backends
	@echo "Extracting backend $*..."
	@CID=$$(docker create local-ai-backend:$*) && \
	  rm -rf local-backends/$* && mkdir -p local-backends/$* && \
	  docker cp $$CID:/ - | tar -xf - -C local-backends/$* && \
	  docker rm $$CID > /dev/null

extract-realtime-backends: $(addprefix extract-backend-,$(REALTIME_BACKEND_NAMES))

test-realtime-models-docker: build-mock-backend
	docker build --target build-requirements \
	  --build-arg BUILD_TYPE=$(or $(BUILD_TYPE),cublas) \
	  --build-arg CUDA_MAJOR_VERSION=$(or $(CUDA_MAJOR_VERSION),13) \
	  --build-arg CUDA_MINOR_VERSION=$(or $(CUDA_MINOR_VERSION),0) \
	  -t localai-test-runner .
	docker run --rm \
	  $(REALTIME_DOCKER_FLAGS) \
	  -v $(abspath ./):/build \
	  -v $(REALTIME_MODELS_DIR):/models:ro \
	  -v $(REALTIME_BACKENDS_DIR):/backends \
	  -v localai-go-cache:/root/go/pkg/mod \
	  -v localai-go-build-cache:/root/.cache/go-build \
	  -e REALTIME_TEST_MODEL=$${REALTIME_TEST_MODEL:-realtime-test-pipeline} \
	  -e REALTIME_VAD=$(REALTIME_VAD) \
	  -e REALTIME_STT=$(REALTIME_STT) \
	  -e REALTIME_LLM=$(REALTIME_LLM) \
	  -e REALTIME_TTS=$(REALTIME_TTS) \
	  -e REALTIME_BACKENDS_PATH=/backends \
	  -e REALTIME_MODELS_PATH=/models \
	  -w /build \
	  localai-test-runner \
	  bash -c 'git config --global --add safe.directory /build && \
	    make protogen-go && make build-mock-backend && \
	    go run github.com/onsi/ginkgo/v2/ginkgo --label-filter="Realtime" --flake-attempts $(TEST_FLAKES) -v -r ./tests/e2e'

test-container:
	docker build --target requirements -t local-ai-test-container .
	docker run -ti --rm --entrypoint /bin/bash -ti -v $(abspath ./):/build local-ai-test-container

########################################################
## Help
########################################################

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

########################################################
## Backends
########################################################

.PHONY: protogen
protogen: protogen-go

protoc:
	@OS_NAME=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	ARCH_NAME=$$(uname -m); \
	if [ "$$OS_NAME" = "darwin" ]; then \
	  if [ "$$ARCH_NAME" = "arm64" ]; then \
	    FILE=protoc-31.1-osx-aarch_64.zip; \
	  elif [ "$$ARCH_NAME" = "x86_64" ]; then \
	    FILE=protoc-31.1-osx-x86_64.zip; \
	  else \
	    echo "Unsupported macOS architecture: $$ARCH_NAME"; exit 1; \
	  fi; \
	elif [ "$$OS_NAME" = "linux" ]; then \
	  if [ "$$ARCH_NAME" = "x86_64" ]; then \
	    FILE=protoc-31.1-linux-x86_64.zip; \
	  elif [ "$$ARCH_NAME" = "aarch64" ] || [ "$$ARCH_NAME" = "arm64" ]; then \
	    FILE=protoc-31.1-linux-aarch_64.zip; \
	  elif [ "$$ARCH_NAME" = "ppc64le" ]; then \
	    FILE=protoc-31.1-linux-ppcle_64.zip; \
	  elif [ "$$ARCH_NAME" = "s390x" ]; then \
	    FILE=protoc-31.1-linux-s390_64.zip; \
	  elif [ "$$ARCH_NAME" = "i386" ] || [ "$$ARCH_NAME" = "x86" ]; then \
	    FILE=protoc-31.1-linux-x86_32.zip; \
	  else \
	    echo "Unsupported Linux architecture: $$ARCH_NAME"; exit 1; \
	  fi; \
	else \
	  echo "Unsupported OS: $$OS_NAME"; exit 1; \
	fi; \
	URL=https://github.com/protocolbuffers/protobuf/releases/download/v31.1/$$FILE; \
	curl -L $$URL -o protoc.zip && \
	unzip -j -d $(CURDIR) protoc.zip bin/protoc && rm protoc.zip

.PHONY: protogen-go
protogen-go: protoc install-go-tools
	mkdir -p pkg/grpc/proto
	./protoc --experimental_allow_proto3_optional -Ibackend/ --go_out=pkg/grpc/proto/ --go_opt=paths=source_relative --go-grpc_out=pkg/grpc/proto/ --go-grpc_opt=paths=source_relative \
    backend/backend.proto

core/config/inference_defaults.json: ## Fetch inference defaults from unsloth (only if missing)
	$(GOCMD) generate ./core/config/...

.PHONY: generate
generate: core/config/inference_defaults.json ## Ensure inference defaults exist

.PHONY: generate-force
generate-force: ## Re-fetch inference defaults from unsloth (always)
	$(GOCMD) generate ./core/config/...

.PHONY: protogen-go-clean
protogen-go-clean:
	$(RM) pkg/grpc/proto/backend.pb.go pkg/grpc/proto/backend_grpc.pb.go
	$(RM) bin/*

prepare-test-extra: protogen-python
	$(MAKE) -C backend/python/transformers
	$(MAKE) -C backend/python/outetts
	$(MAKE) -C backend/python/diffusers
	$(MAKE) -C backend/python/chatterbox
	$(MAKE) -C backend/python/vllm
	$(MAKE) -C backend/python/vllm-omni
	$(MAKE) -C backend/python/vibevoice
	$(MAKE) -C backend/python/moonshine
	$(MAKE) -C backend/python/pocket-tts
	$(MAKE) -C backend/python/qwen-tts
	$(MAKE) -C backend/python/fish-speech
	$(MAKE) -C backend/python/faster-qwen3-tts
	$(MAKE) -C backend/python/qwen-asr
	$(MAKE) -C backend/python/nemo
	$(MAKE) -C backend/python/voxcpm
	$(MAKE) -C backend/python/whisperx
	$(MAKE) -C backend/python/ace-step
	$(MAKE) -C backend/python/trl

test-extra: prepare-test-extra
	$(MAKE) -C backend/python/transformers test
	$(MAKE) -C backend/python/outetts test
	$(MAKE) -C backend/python/diffusers test
	$(MAKE) -C backend/python/chatterbox test
	$(MAKE) -C backend/python/vllm test
	$(MAKE) -C backend/python/vllm-omni test
	$(MAKE) -C backend/python/vibevoice test
	$(MAKE) -C backend/python/moonshine test
	$(MAKE) -C backend/python/pocket-tts test
	$(MAKE) -C backend/python/qwen-tts test
	$(MAKE) -C backend/python/fish-speech test
	$(MAKE) -C backend/python/faster-qwen3-tts test
	$(MAKE) -C backend/python/qwen-asr test
	$(MAKE) -C backend/python/nemo test
	$(MAKE) -C backend/python/voxcpm test
	$(MAKE) -C backend/python/whisperx test
	$(MAKE) -C backend/python/ace-step test
	$(MAKE) -C backend/python/trl test

DOCKER_IMAGE?=local-ai
IMAGE_TYPE?=core
BASE_IMAGE?=ubuntu:24.04

docker:
	docker build \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="$(GO_TAGS)" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		--build-arg BUILD_TYPE=$(BUILD_TYPE) \
		--build-arg CUDA_MAJOR_VERSION=$(CUDA_MAJOR_VERSION) \
		--build-arg CUDA_MINOR_VERSION=$(CUDA_MINOR_VERSION) \
		--build-arg UBUNTU_VERSION=$(UBUNTU_VERSION) \
		--build-arg UBUNTU_CODENAME=$(UBUNTU_CODENAME) \
		-t $(DOCKER_IMAGE) .

docker-cuda12:
	docker build \
		--build-arg CUDA_MAJOR_VERSION=${CUDA_MAJOR_VERSION} \
		--build-arg CUDA_MINOR_VERSION=${CUDA_MINOR_VERSION} \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="$(GO_TAGS)" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		--build-arg BUILD_TYPE=$(BUILD_TYPE) \
		--build-arg UBUNTU_VERSION=$(UBUNTU_VERSION) \
		--build-arg UBUNTU_CODENAME=$(UBUNTU_CODENAME) \
		-t $(DOCKER_IMAGE)-cuda-12 .

docker-image-intel:
	docker build \
		--build-arg BASE_IMAGE=intel/oneapi-basekit:2025.3.0-0-devel-ubuntu24.04 \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="$(GO_TAGS)" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		--build-arg BUILD_TYPE=intel \
		--build-arg CUDA_MAJOR_VERSION=$(CUDA_MAJOR_VERSION) \
		--build-arg CUDA_MINOR_VERSION=$(CUDA_MINOR_VERSION) \
		--build-arg UBUNTU_VERSION=$(UBUNTU_VERSION) \
		--build-arg UBUNTU_CODENAME=$(UBUNTU_CODENAME) \
		-t $(DOCKER_IMAGE) .

########################################################
## Backends
########################################################

# Pattern rule for standard backends (docker-based)
# This matches all backends that use docker-build-* and docker-save-*
backends/%: docker-build-% docker-save-% build
	./local-ai backends install "ocifile://$(abspath ./backend-images/$*.tar)"

# Darwin-specific backends (keep as explicit targets since they have special build logic)
backends/llama-cpp-darwin: build
	bash ./scripts/build/llama-cpp-darwin.sh
	./local-ai backends install "ocifile://$(abspath ./backend-images/llama-cpp.tar)"

build-darwin-python-backend: build
	bash ./scripts/build/python-darwin.sh

build-darwin-go-backend: build
	bash ./scripts/build/golang-darwin.sh

backends/mlx:
	BACKEND=mlx $(MAKE) build-darwin-python-backend
	./local-ai backends install "ocifile://$(abspath ./backend-images/mlx.tar)"

backends/diffuser-darwin:
	BACKEND=diffusers $(MAKE) build-darwin-python-backend
	./local-ai backends install "ocifile://$(abspath ./backend-images/diffusers.tar)"

backends/mlx-vlm:
	BACKEND=mlx-vlm $(MAKE) build-darwin-python-backend
	./local-ai backends install "ocifile://$(abspath ./backend-images/mlx-vlm.tar)"

backends/mlx-audio:
	BACKEND=mlx-audio $(MAKE) build-darwin-python-backend
	./local-ai backends install "ocifile://$(abspath ./backend-images/mlx-audio.tar)"

backends/mlx-distributed:
	BACKEND=mlx-distributed $(MAKE) build-darwin-python-backend
	./local-ai backends install "ocifile://$(abspath ./backend-images/mlx-distributed.tar)"

backends/stablediffusion-ggml-darwin:
	BACKEND=stablediffusion-ggml BUILD_TYPE=metal $(MAKE) build-darwin-go-backend
	./local-ai backends install "ocifile://$(abspath ./backend-images/stablediffusion-ggml.tar)"

backend-images:
	mkdir -p backend-images

# Backend metadata: BACKEND_NAME | DOCKERFILE_TYPE | BUILD_CONTEXT | PROGRESS_FLAG | NEEDS_BACKEND_ARG
# llama-cpp is special - uses llama-cpp Dockerfile and doesn't need BACKEND arg
BACKEND_LLAMA_CPP = llama-cpp|llama-cpp|.|false|false

# Golang backends
BACKEND_PIPER = piper|golang|.|false|true
BACKEND_LOCAL_STORE = local-store|golang|.|false|true
BACKEND_HUGGINGFACE = huggingface|golang|.|false|true
BACKEND_AVIAN = avian|golang|.|false|true
BACKEND_SILERO_VAD = silero-vad|golang|.|false|true
BACKEND_STABLEDIFFUSION_GGML = stablediffusion-ggml|golang|.|--progress=plain|true
BACKEND_WHISPER = whisper|golang|.|false|true
BACKEND_VOXTRAL = voxtral|golang|.|false|true
BACKEND_ACESTEP_CPP = acestep-cpp|golang|.|false|true
BACKEND_OPUS = opus|golang|.|false|true

# Python backends with root context
BACKEND_RERANKERS = rerankers|python|.|false|true
BACKEND_TRANSFORMERS = transformers|python|.|false|true
BACKEND_OUTETTS = outetts|python|.|false|true
BACKEND_FASTER_WHISPER = faster-whisper|python|.|false|true
BACKEND_COQUI = coqui|python|.|false|true
BACKEND_RFDETR = rfdetr|python|.|false|true
BACKEND_KITTEN_TTS = kitten-tts|python|.|false|true
BACKEND_NEUTTS = neutts|python|.|false|true
BACKEND_KOKORO = kokoro|python|.|false|true
BACKEND_VLLM = vllm|python|.|false|true
BACKEND_VLLM_OMNI = vllm-omni|python|.|false|true
BACKEND_DIFFUSERS = diffusers|python|.|--progress=plain|true
BACKEND_CHATTERBOX = chatterbox|python|.|false|true
BACKEND_VIBEVOICE = vibevoice|python|.|--progress=plain|true
BACKEND_MOONSHINE = moonshine|python|.|false|true
BACKEND_POCKET_TTS = pocket-tts|python|.|false|true
BACKEND_QWEN_TTS = qwen-tts|python|.|false|true
BACKEND_FISH_SPEECH = fish-speech|python|.|false|true
BACKEND_FASTER_QWEN3_TTS = faster-qwen3-tts|python|.|false|true
BACKEND_QWEN_ASR = qwen-asr|python|.|false|true
BACKEND_NEMO = nemo|python|.|false|true
BACKEND_VOXCPM = voxcpm|python|.|false|true
BACKEND_WHISPERX = whisperx|python|.|false|true
BACKEND_ACE_STEP = ace-step|python|.|false|true
BACKEND_MLX_DISTRIBUTED = mlx-distributed|python|./|false|true
BACKEND_TRL = trl|python|.|false|true
BACKEND_LLAMA_CPP_QUANTIZATION = llama-cpp-quantization|python|.|false|true

# Helper function to build docker image for a backend
# Usage: $(call docker-build-backend,BACKEND_NAME,DOCKERFILE_TYPE,BUILD_CONTEXT,PROGRESS_FLAG,NEEDS_BACKEND_ARG)
define docker-build-backend
	docker build $(if $(filter-out false,$(4)),$(4)) \
		--build-arg BUILD_TYPE=$(BUILD_TYPE) \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		--build-arg CUDA_MAJOR_VERSION=$(CUDA_MAJOR_VERSION) \
		--build-arg CUDA_MINOR_VERSION=$(CUDA_MINOR_VERSION) \
		--build-arg UBUNTU_VERSION=$(UBUNTU_VERSION) \
		--build-arg UBUNTU_CODENAME=$(UBUNTU_CODENAME) \
		$(if $(filter true,$(5)),--build-arg BACKEND=$(1)) \
		-t local-ai-backend:$(1) -f backend/Dockerfile.$(2) $(3)
endef

# Generate docker-build targets from backend definitions
define generate-docker-build-target
docker-build-$(word 1,$(subst |, ,$(1))):
	$$(call docker-build-backend,$(word 1,$(subst |, ,$(1))),$(word 2,$(subst |, ,$(1))),$(word 3,$(subst |, ,$(1))),$(word 4,$(subst |, ,$(1))),$(word 5,$(subst |, ,$(1))))
endef

# Generate all docker-build targets
$(eval $(call generate-docker-build-target,$(BACKEND_LLAMA_CPP)))
$(eval $(call generate-docker-build-target,$(BACKEND_PIPER)))
$(eval $(call generate-docker-build-target,$(BACKEND_LOCAL_STORE)))
$(eval $(call generate-docker-build-target,$(BACKEND_HUGGINGFACE)))
$(eval $(call generate-docker-build-target,$(BACKEND_AVIAN)))
$(eval $(call generate-docker-build-target,$(BACKEND_SILERO_VAD)))
$(eval $(call generate-docker-build-target,$(BACKEND_STABLEDIFFUSION_GGML)))
$(eval $(call generate-docker-build-target,$(BACKEND_WHISPER)))
$(eval $(call generate-docker-build-target,$(BACKEND_VOXTRAL)))
$(eval $(call generate-docker-build-target,$(BACKEND_OPUS)))
$(eval $(call generate-docker-build-target,$(BACKEND_RERANKERS)))
$(eval $(call generate-docker-build-target,$(BACKEND_TRANSFORMERS)))
$(eval $(call generate-docker-build-target,$(BACKEND_OUTETTS)))
$(eval $(call generate-docker-build-target,$(BACKEND_FASTER_WHISPER)))
$(eval $(call generate-docker-build-target,$(BACKEND_COQUI)))
$(eval $(call generate-docker-build-target,$(BACKEND_RFDETR)))
$(eval $(call generate-docker-build-target,$(BACKEND_KITTEN_TTS)))
$(eval $(call generate-docker-build-target,$(BACKEND_NEUTTS)))
$(eval $(call generate-docker-build-target,$(BACKEND_KOKORO)))
$(eval $(call generate-docker-build-target,$(BACKEND_VLLM)))
$(eval $(call generate-docker-build-target,$(BACKEND_VLLM_OMNI)))
$(eval $(call generate-docker-build-target,$(BACKEND_DIFFUSERS)))
$(eval $(call generate-docker-build-target,$(BACKEND_CHATTERBOX)))
$(eval $(call generate-docker-build-target,$(BACKEND_VIBEVOICE)))
$(eval $(call generate-docker-build-target,$(BACKEND_MOONSHINE)))
$(eval $(call generate-docker-build-target,$(BACKEND_POCKET_TTS)))
$(eval $(call generate-docker-build-target,$(BACKEND_QWEN_TTS)))
$(eval $(call generate-docker-build-target,$(BACKEND_FISH_SPEECH)))
$(eval $(call generate-docker-build-target,$(BACKEND_FASTER_QWEN3_TTS)))
$(eval $(call generate-docker-build-target,$(BACKEND_QWEN_ASR)))
$(eval $(call generate-docker-build-target,$(BACKEND_NEMO)))
$(eval $(call generate-docker-build-target,$(BACKEND_VOXCPM)))
$(eval $(call generate-docker-build-target,$(BACKEND_WHISPERX)))
$(eval $(call generate-docker-build-target,$(BACKEND_ACE_STEP)))
$(eval $(call generate-docker-build-target,$(BACKEND_ACESTEP_CPP)))
$(eval $(call generate-docker-build-target,$(BACKEND_MLX_DISTRIBUTED)))
$(eval $(call generate-docker-build-target,$(BACKEND_TRL)))
$(eval $(call generate-docker-build-target,$(BACKEND_LLAMA_CPP_QUANTIZATION)))

# Pattern rule for docker-save targets
docker-save-%: backend-images
	docker save local-ai-backend:$* -o backend-images/$*.tar

docker-build-backends: docker-build-llama-cpp docker-build-rerankers docker-build-vllm docker-build-vllm-omni docker-build-transformers docker-build-outetts docker-build-diffusers docker-build-kokoro docker-build-faster-whisper docker-build-coqui docker-build-chatterbox docker-build-vibevoice docker-build-moonshine docker-build-pocket-tts docker-build-qwen-tts docker-build-fish-speech docker-build-faster-qwen3-tts docker-build-qwen-asr docker-build-nemo docker-build-voxcpm docker-build-whisperx docker-build-ace-step docker-build-acestep-cpp docker-build-voxtral docker-build-mlx-distributed docker-build-trl docker-build-llama-cpp-quantization docker-build-avian

########################################################
### Mock Backend for E2E Tests
########################################################

build-mock-backend: protogen-go
	$(GOCMD) build -o tests/e2e/mock-backend/mock-backend ./tests/e2e/mock-backend

clean-mock-backend:
	rm -f tests/e2e/mock-backend/mock-backend

########################################################
### UI E2E Test Server
########################################################

build-ui-test-server: build-mock-backend react-ui protogen-go
	$(GOCMD) build -o tests/e2e-ui/ui-test-server ./tests/e2e-ui

test-ui-e2e: build-ui-test-server
	cd core/http/react-ui && npm install && npx playwright install --with-deps chromium && npx playwright test

test-ui-e2e-docker:
	docker build -t localai-ui-e2e -f tests/e2e-ui/Dockerfile .
	docker run --rm localai-ui-e2e

clean-ui-test-server:
	rm -f tests/e2e-ui/ui-test-server

########################################################
### END Backends
########################################################

.PHONY: swagger
swagger:
	swag init -g core/http/app.go --output swagger

# DEPRECATED: gen-assets is for the legacy Alpine.js UI. Remove when legacy UI is removed.
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

########################################################
## Platform-specific builds
########################################################

## fyne cross-platform build
build-launcher-darwin: build-launcher
	go run github.com/tiagomelo/macos-dmg-creator/cmd/createdmg@latest \
	--appName "LocalAI" \
	--appBinaryPath "$(LAUNCHER_BINARY_NAME)" \
	--bundleIdentifier "com.localai.launcher" \
	--iconPath "core/http/static/logo.png" \
	--outputDir "dist/"

build-launcher-linux:
	cd cmd/launcher && go run fyne.io/tools/cmd/fyne@latest package -os linux -icon ../../core/http/static/logo.png --executable $(LAUNCHER_BINARY_NAME)-linux && mv launcher.tar.xz ../../$(LAUNCHER_BINARY_NAME)-linux.tar.xz
