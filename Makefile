GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_NAME=local-ai

GORELEASER?=

export BUILD_TYPE?=

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

## Build:
build: protogen-go install-go-tools ## Build the project
	$(info ${GREEN}I local-ai build info:${RESET})
	$(info ${GREEN}I BUILD_TYPE: ${YELLOW}$(BUILD_TYPE)${RESET})
	$(info ${GREEN}I GO_TAGS: ${YELLOW}$(GO_TAGS)${RESET})
	$(info ${GREEN}I LD_FLAGS: ${YELLOW}$(LD_FLAGS)${RESET})
	$(info ${GREEN}I UPX: ${YELLOW}$(UPX)${RESET})
	rm -rf $(BINARY_NAME) || true
	CGO_LDFLAGS="$(CGO_LDFLAGS)" $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o $(BINARY_NAME) ./

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
	mkdir test-models
	mkdir test-dir
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
	HUGGINGFACE_GRPC=$(abspath ./)/backend/python/transformers/run.sh TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models BACKENDS_PATH=$(abspath ./)/backends \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --label-filter="!llama-gguf"  --flake-attempts $(TEST_FLAKES) --fail-fast -v -r $(TEST_PATHS)
	$(MAKE) test-llama-gguf
	$(MAKE) test-tts
	$(MAKE) test-stablediffusion

backends/llama-cpp: docker-build-llama-cpp docker-save-llama-cpp build
	./local-ai backends install "ocifile://$(abspath ./backend-images/llama-cpp.tar)"

backends/piper: docker-build-piper docker-save-piper build
	./local-ai backends install "ocifile://$(abspath ./backend-images/piper.tar)"

backends/stablediffusion-ggml: docker-build-stablediffusion-ggml docker-save-stablediffusion-ggml build
	./local-ai backends install "ocifile://$(abspath ./backend-images/stablediffusion-ggml.tar)"

backends/whisper: docker-build-whisper docker-save-whisper build
	./local-ai backends install "ocifile://$(abspath ./backend-images/whisper.tar)"

backends/silero-vad: docker-build-silero-vad docker-save-silero-vad build
	./local-ai backends install "ocifile://$(abspath ./backend-images/silero-vad.tar)"

backends/local-store: docker-build-local-store docker-save-local-store build
	./local-ai backends install "ocifile://$(abspath ./backend-images/local-store.tar)"

backends/huggingface: docker-build-huggingface docker-save-huggingface build
	./local-ai backends install "ocifile://$(abspath ./backend-images/huggingface.tar)"

backends/rfdetr: docker-build-rfdetr docker-save-rfdetr build
	./local-ai backends install "ocifile://$(abspath ./backend-images/rfdetr.tar)"

backends/kitten-tts: docker-build-kitten-tts docker-save-kitten-tts build
	./local-ai backends install "ocifile://$(abspath ./backend-images/kitten-tts.tar)"

backends/kokoro: docker-build-kokoro docker-save-kokoro build
	./local-ai backends install "ocifile://$(abspath ./backend-images/kokoro.tar)"

########################################################
## AIO tests
########################################################

docker-build-aio:
	docker build --build-arg MAKEFLAGS="--jobs=5 --output-sync=target" -t local-ai:tests -f Dockerfile .
	BASE_IMAGE=local-ai:tests DOCKER_AIO_IMAGE=local-ai-aio:test $(MAKE) docker-aio

e2e-aio:
	LOCALAI_BACKEND_DIR=$(abspath ./backends) \
	LOCALAI_MODELS_DIR=$(abspath ./models) \
	LOCALAI_IMAGE_TAG=test \
	LOCALAI_IMAGE=local-ai-aio \
	$(MAKE) run-e2e-aio

run-e2e-aio: protogen-go
	@echo 'Running e2e AIO tests'
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r ./tests/e2e-aio

########################################################
## E2E tests
########################################################

prepare-e2e:
	mkdir -p $(TEST_DIR)
	cp -rfv $(abspath ./tests/e2e-fixtures)/gpu.yaml $(TEST_DIR)/gpu.yaml
	test -e $(TEST_DIR)/ggllm-test-model.bin || wget -q https://huggingface.co/TheBloke/CodeLlama-7B-Instruct-GGUF/resolve/main/codellama-7b-instruct.Q2_K.gguf -O $(TEST_DIR)/ggllm-test-model.bin
	docker build --build-arg IMAGE_TYPE=core --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg CUDA_MAJOR_VERSION=12 --build-arg CUDA_MINOR_VERSION=0 -t localai-tests .

run-e2e-image:
	ls -liah $(abspath ./tests/e2e-fixtures)
	docker run -p 5390:8080 -e MODELS_PATH=/models -e THREADS=1 -e DEBUG=true -d --rm -v $(TEST_DIR):/models --gpus all --name e2e-tests-$(RANDOM) localai-tests

test-e2e:
	@echo 'Running e2e tests'
	BUILD_TYPE=$(BUILD_TYPE) \
	LOCALAI_API=http://$(E2E_BRIDGE_IP):5390/v1 \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r ./tests/e2e

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
	curl -L -s $$URL -o protoc.zip && \
	unzip -j -d $(CURDIR) protoc.zip bin/protoc && rm protoc.zip

.PHONY: protogen-go
protogen-go: protoc install-go-tools
	mkdir -p pkg/grpc/proto
	./protoc --experimental_allow_proto3_optional -Ibackend/ --go_out=pkg/grpc/proto/ --go_opt=paths=source_relative --go-grpc_out=pkg/grpc/proto/ --go-grpc_opt=paths=source_relative \
    backend/backend.proto

.PHONY: protogen-go-clean
protogen-go-clean:
	$(RM) pkg/grpc/proto/backend.pb.go pkg/grpc/proto/backend_grpc.pb.go
	$(RM) bin/*

prepare-test-extra: protogen-python
	$(MAKE) -C backend/python/transformers
	$(MAKE) -C backend/python/diffusers
	$(MAKE) -C backend/python/chatterbox
	$(MAKE) -C backend/python/vllm

test-extra: prepare-test-extra
	$(MAKE) -C backend/python/transformers test
	$(MAKE) -C backend/python/diffusers test
	$(MAKE) -C backend/python/chatterbox test
	$(MAKE) -C backend/python/vllm test

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
		-t $(DOCKER_IMAGE)-cuda-11 .

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
		--build-arg BASE_IMAGE=quay.io/go-skynet/intel-oneapi-base:latest \
		--build-arg IMAGE_TYPE=$(IMAGE_TYPE) \
		--build-arg GO_TAGS="$(GO_TAGS)" \
		--build-arg MAKEFLAGS="$(DOCKER_MAKEFLAGS)" \
		--build-arg BUILD_TYPE=intel -t $(DOCKER_IMAGE) .

########################################################
## Backends
########################################################

backend-images:
	mkdir -p backend-images

docker-build-llama-cpp:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:llama-cpp -f backend/Dockerfile.llama-cpp .

docker-build-bark-cpp:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:bark-cpp -f backend/Dockerfile.golang --build-arg BACKEND=bark-cpp .

docker-build-piper:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:piper -f backend/Dockerfile.golang --build-arg BACKEND=piper .

docker-build-local-store:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:local-store -f backend/Dockerfile.golang --build-arg BACKEND=local-store .

docker-build-huggingface:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:huggingface -f backend/Dockerfile.golang --build-arg BACKEND=huggingface .

docker-build-rfdetr:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:rfdetr -f backend/Dockerfile.python --build-arg BACKEND=rfdetr ./backend

docker-build-kitten-tts:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:kitten-tts -f backend/Dockerfile.python --build-arg BACKEND=kitten-tts ./backend

docker-save-kitten-tts: backend-images
	docker save local-ai-backend:kitten-tts -o backend-images/kitten-tts.tar

docker-build-kokoro:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:kokoro -f backend/Dockerfile.python --build-arg BACKEND=kokoro ./backend

docker-save-kokoro: backend-images
	docker save local-ai-backend:kokoro -o backend-images/kokoro.tar

docker-save-rfdetr: backend-images
	docker save local-ai-backend:rfdetr -o backend-images/rfdetr.tar

docker-save-huggingface: backend-images
	docker save local-ai-backend:huggingface -o backend-images/huggingface.tar

docker-save-local-store: backend-images
	docker save local-ai-backend:local-store -o backend-images/local-store.tar

docker-build-silero-vad:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:silero-vad -f backend/Dockerfile.golang --build-arg BACKEND=silero-vad .

docker-save-silero-vad: backend-images
	docker save local-ai-backend:silero-vad -o backend-images/silero-vad.tar

docker-save-piper: backend-images
	docker save local-ai-backend:piper -o backend-images/piper.tar

docker-save-llama-cpp: backend-images
	docker save local-ai-backend:llama-cpp -o backend-images/llama-cpp.tar

docker-save-bark-cpp: backend-images
	docker save local-ai-backend:bark-cpp -o backend-images/bark-cpp.tar

docker-build-stablediffusion-ggml:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:stablediffusion-ggml -f backend/Dockerfile.golang --build-arg BACKEND=stablediffusion-ggml .

docker-save-stablediffusion-ggml: backend-images
	docker save local-ai-backend:stablediffusion-ggml -o backend-images/stablediffusion-ggml.tar

docker-build-rerankers:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:rerankers -f backend/Dockerfile.python --build-arg BACKEND=rerankers .

docker-build-vllm:
ifeq ($(BUILD_TYPE),)
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:vllm -f backend/Dockerfile.vllmcpu --build-arg BACKEND=vllm .
else
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:vllm -f backend/Dockerfile.python --build-arg BACKEND=vllm .
endif

docker-build-transformers:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:transformers -f backend/Dockerfile.python --build-arg BACKEND=transformers .

docker-build-diffusers:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:diffusers -f backend/Dockerfile.python --build-arg BACKEND=diffusers .

docker-build-whisper:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:whisper -f backend/Dockerfile.golang --build-arg BACKEND=whisper  .

docker-save-whisper: backend-images
	docker save local-ai-backend:whisper -o backend-images/whisper.tar

docker-build-faster-whisper:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:faster-whisper -f backend/Dockerfile.python --build-arg BACKEND=faster-whisper .

docker-build-coqui:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:coqui -f backend/Dockerfile.python --build-arg BACKEND=coqui .

docker-build-bark:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:bark -f backend/Dockerfile.python --build-arg BACKEND=bark .

docker-build-chatterbox:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:chatterbox -f backend/Dockerfile.python --build-arg BACKEND=chatterbox .

docker-build-exllama2:
	docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:exllama2 -f backend/Dockerfile.python --build-arg BACKEND=exllama2 .

docker-build-backends: docker-build-llama-cpp docker-build-rerankers docker-build-vllm docker-build-transformers docker-build-diffusers docker-build-kokoro docker-build-faster-whisper docker-build-coqui docker-build-bark docker-build-chatterbox docker-build-exllama2

########################################################
### END Backends
########################################################

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
