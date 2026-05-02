# Disable parallel execution for backend builds
.NOTPARALLEL: backends/diffusers backends/llama-cpp backends/turboquant backends/outetts backends/piper backends/stablediffusion-ggml backends/whisper backends/faster-whisper backends/silero-vad backends/local-store backends/huggingface backends/rfdetr backends/insightface backends/speaker-recognition backends/kitten-tts backends/kokoro backends/chatterbox backends/llama-cpp-darwin backends/neutts build-darwin-python-backend build-darwin-go-backend backends/mlx backends/diffuser-darwin backends/mlx-vlm backends/mlx-audio backends/mlx-distributed backends/stablediffusion-ggml-darwin backends/vllm backends/vllm-omni backends/sglang backends/moonshine backends/pocket-tts backends/qwen-tts backends/faster-qwen3-tts backends/qwen-asr backends/nemo backends/voxcpm backends/whisperx backends/ace-step backends/acestep-cpp backends/fish-speech backends/voxtral backends/opus backends/trl backends/llama-cpp-quantization backends/kokoros backends/sam3-cpp backends/qwen3-tts-cpp backends/vibevoice-cpp backends/tinygrad backends/sherpa-onnx

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


.PHONY: all test build vendor lint lint-all

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
	rm -f tests/e2e/mock-backend/mock-backend

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

prepare-test: protogen-go build-mock-backend

########################################################
## Tests
########################################################

## Test targets
## After the test-suite reorg (see plans/test-reorg) the default `make test`
## no longer downloads multi-GB GGUF/whisper fixtures or builds llama-cpp /
## transformers / piper / whisper / stablediffusion-ggml. core/http/app_test.go
## now drives the mock-backend binary built by build-mock-backend; real-backend
## inference moved into tests/e2e-backends/ (per-backend, path-filtered) and
## tests/e2e-aio/ (nightly).
test: prepare-test
	@echo 'Running tests'
	export GO_TAGS="debug"
	OPUS_SHIM_LIBRARY=$(abspath ./pkg/opus/shim/libopusshim.so) \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) --fail-fast -v -r $(TEST_PATHS)

########################################################
## Lint
########################################################
## Runs golangci-lint with config from .golangci.yml. Includes the standard
## linter set plus forbidigo, which enforces the Ginkgo/Gomega-only test
## convention documented in .agents/coding-style.md.
##
## LINT_EXCLUDE_DIRS_RE matches directories whose Go packages can't typecheck
## without C/C++ headers we don't install in the lint runner (cgo wrappers
## around llama.cpp, piper/spdlog, silero-vad/onnxruntime, and Fyne/OpenGL for
## the launcher). Their compile-time correctness is enforced by their own
## build pipelines. Keep this as a deny list — `go list ./...` discovers
## everything else automatically, so new packages are scanned by default.
LINT_EXCLUDE_DIRS_RE=/(backend/go/(piper|silero-vad|llm)|cmd/launcher)(/|$$)

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo 'golangci-lint not installed. Install: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest'; \
		exit 1; \
	}
	golangci-lint run $$(go list -e -f '{{.Dir}}' ./... | grep -vE '$(LINT_EXCLUDE_DIRS_RE)')

## Like `lint` but reports every issue, including the pre-existing baseline
## that `lint` ignores via .golangci.yml's new-from-merge-base. Use this to
## see what's available to clean up.
lint-all:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo 'golangci-lint not installed. Install: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest'; \
		exit 1; \
	}
	golangci-lint run --new=false --new-from-merge-base= --new-from-rev= $$(go list -e -f '{{.Dir}}' ./... | grep -vE '$(LINT_EXCLUDE_DIRS_RE)')

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

## Storage / vector-store integration. Requires the local-store backend to
## be available — we build it on demand and pass its location via
## BACKENDS_PATH (the model loader looks there for the gRPC binary).
test-stores: backends/local-store
	BACKENDS_PATH=$(abspath ./)/backends \
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r tests/integration

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

# Container-based real-model realtime testing. Build env vars / pipeline
# definition kept here so test-realtime-models-docker can drive a fully wired
# pipeline (VAD + STT + LLM + TTS) from inside a containerised runner.
REALTIME_VAD?=silero-vad-ggml
REALTIME_STT?=whisper-1
REALTIME_LLM?=qwen3-0.6b
REALTIME_TTS?=tts-1

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
	# install-go-tools writes protoc-gen-go and protoc-gen-go-grpc into
	# $(shell go env GOPATH)/bin, which isn't on every dev's PATH. protoc
	# resolves its code-gen plugins via PATH, so without this prefix the
	# generate step fails with "protoc-gen-go: program not found". Prepend
	# GOPATH/bin so the freshly-installed plugins win without requiring a
	# shell-profile change.
	PATH="$$(go env GOPATH)/bin:$$PATH" ./protoc --experimental_allow_proto3_optional -Ibackend/ --go_out=pkg/grpc/proto/ --go_opt=paths=source_relative --go-grpc_out=pkg/grpc/proto/ --go-grpc_opt=paths=source_relative \
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
	$(MAKE) -C backend/python/sglang
	$(MAKE) -C backend/python/vibevoice
	$(MAKE) -C backend/python/moonshine
	$(MAKE) -C backend/python/pocket-tts
	$(MAKE) -C backend/python/qwen-tts
	$(MAKE) -C backend/python/fish-speech
	$(MAKE) -C backend/python/faster-qwen3-tts
	$(MAKE) -C backend/python/qwen-asr
	$(MAKE) -C backend/python/nemo
	$(MAKE) -C backend/python/voxcpm
	$(MAKE) -C backend/python/faster-whisper
	$(MAKE) -C backend/python/whisperx
	$(MAKE) -C backend/python/ace-step
	$(MAKE) -C backend/python/trl
	$(MAKE) -C backend/python/tinygrad
	$(MAKE) -C backend/python/insightface
	$(MAKE) -C backend/python/speaker-recognition
	$(MAKE) -C backend/rust/kokoros kokoros-grpc

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
	$(MAKE) -C backend/python/faster-whisper test
	$(MAKE) -C backend/python/whisperx test
	$(MAKE) -C backend/python/ace-step test
	$(MAKE) -C backend/python/trl test
	$(MAKE) -C backend/python/tinygrad test
	$(MAKE) -C backend/python/insightface test
	$(MAKE) -C backend/python/speaker-recognition test
	$(MAKE) -C backend/rust/kokoros test

##
## End-to-end gRPC tests that exercise a built backend container image.
##
## The test suite in tests/e2e-backends is backend-agnostic. You drive it via env
## vars (see tests/e2e-backends/backend_test.go for the full list) and the
## capability-driven harness picks which gRPC RPCs to exercise:
##
##   BACKEND_IMAGE            Required. Docker image to test, e.g. local-ai-backend:llama-cpp.
##   BACKEND_TEST_MODEL_URL   URL of a model file to download and load.
##   BACKEND_TEST_MODEL_FILE  Path to an already-downloaded model (skips download).
##   BACKEND_TEST_MODEL_NAME  HuggingFace repo id (e.g. Qwen/Qwen2.5-0.5B-Instruct).
##                            Use this instead of MODEL_URL for backends that
##                            resolve HF model ids natively (vllm, vllm-omni).
##   BACKEND_TEST_CAPS        Comma-separated capabilities, default "health,load,predict,stream".
##                            Adds "tools" to exercise ChatDelta tool call extraction.
##   BACKEND_TEST_PROMPT      Override the prompt used in predict/stream specs.
##   BACKEND_TEST_OPTIONS     Comma-separated Options[] entries forwarded to LoadModel,
##                            e.g. "tool_parser:hermes,reasoning_parser:qwen3".
##
## Direct usage (image already built, no docker-build-* dependency):
##
##   make test-extra-backend BACKEND_IMAGE=local-ai-backend:llama-cpp \
##       BACKEND_TEST_MODEL_URL=https://.../model.gguf
##
## Convenience wrappers below build a specific backend image first, then run the
## suite against it.
##
BACKEND_TEST_MODEL_URL?=https://huggingface.co/Qwen/Qwen3-0.6B-GGUF/resolve/main/Qwen3-0.6B-Q8_0.gguf

## Generic target — runs the suite against whatever BACKEND_IMAGE points at.
## Depends on protogen-go so pkg/grpc/proto is generated before `go test`.
test-extra-backend: protogen-go
	@test -n "$$BACKEND_IMAGE" || { echo "BACKEND_IMAGE must be set" >&2; exit 1; }
	BACKEND_IMAGE="$$BACKEND_IMAGE" \
	BACKEND_TEST_MODEL_URL="$${BACKEND_TEST_MODEL_URL:-$(BACKEND_TEST_MODEL_URL)}" \
	BACKEND_TEST_MODEL_FILE="$$BACKEND_TEST_MODEL_FILE" \
	BACKEND_TEST_MODEL_NAME="$$BACKEND_TEST_MODEL_NAME" \
	BACKEND_TEST_MMPROJ_URL="$$BACKEND_TEST_MMPROJ_URL" \
	BACKEND_TEST_MMPROJ_FILE="$$BACKEND_TEST_MMPROJ_FILE" \
	BACKEND_TEST_AUDIO_URL="$$BACKEND_TEST_AUDIO_URL" \
	BACKEND_TEST_AUDIO_FILE="$$BACKEND_TEST_AUDIO_FILE" \
	BACKEND_TEST_CAPS="$$BACKEND_TEST_CAPS" \
	BACKEND_TEST_PROMPT="$$BACKEND_TEST_PROMPT" \
	BACKEND_TEST_OPTIONS="$$BACKEND_TEST_OPTIONS" \
	BACKEND_TEST_TOOL_PROMPT="$$BACKEND_TEST_TOOL_PROMPT" \
	BACKEND_TEST_TOOL_NAME="$$BACKEND_TEST_TOOL_NAME" \
	BACKEND_TEST_CACHE_TYPE_K="$$BACKEND_TEST_CACHE_TYPE_K" \
	BACKEND_TEST_CACHE_TYPE_V="$$BACKEND_TEST_CACHE_TYPE_V" \
	BACKEND_TEST_FACE_IMAGE_1_URL="$$BACKEND_TEST_FACE_IMAGE_1_URL" \
	BACKEND_TEST_FACE_IMAGE_1_FILE="$$BACKEND_TEST_FACE_IMAGE_1_FILE" \
	BACKEND_TEST_FACE_IMAGE_2_URL="$$BACKEND_TEST_FACE_IMAGE_2_URL" \
	BACKEND_TEST_FACE_IMAGE_2_FILE="$$BACKEND_TEST_FACE_IMAGE_2_FILE" \
	BACKEND_TEST_FACE_IMAGE_3_URL="$$BACKEND_TEST_FACE_IMAGE_3_URL" \
	BACKEND_TEST_FACE_IMAGE_3_FILE="$$BACKEND_TEST_FACE_IMAGE_3_FILE" \
	BACKEND_TEST_VERIFY_DISTANCE_CEILING="$$BACKEND_TEST_VERIFY_DISTANCE_CEILING" \
	go test -v -timeout 30m ./tests/e2e-backends/...

## Convenience wrappers: build the image, then exercise it.
test-extra-backend-llama-cpp: docker-build-llama-cpp
	BACKEND_IMAGE=local-ai-backend:llama-cpp \
	BACKEND_TEST_CAPS=health,load,predict,stream,logprobs,logit_bias \
	$(MAKE) test-extra-backend

test-extra-backend-ik-llama-cpp: docker-build-ik-llama-cpp
	BACKEND_IMAGE=local-ai-backend:ik-llama-cpp $(MAKE) test-extra-backend

## turboquant: exercises the llama.cpp-fork backend with the fork's
## *TurboQuant-specific* KV-cache types (turbo3 for both K and V). turbo3
## is what makes this backend distinct from stock llama-cpp — picking q8_0
## here would only test the standard llama.cpp code path that the upstream
## llama-cpp backend already covers. The fork auto-enables flash_attention
## when turbo3/turbo4 are active, so we don't need to set it explicitly.
test-extra-backend-turboquant: docker-build-turboquant
	BACKEND_IMAGE=local-ai-backend:turboquant \
	BACKEND_TEST_CACHE_TYPE_K=q8_0 \
	BACKEND_TEST_CACHE_TYPE_V=turbo3 \
	$(MAKE) test-extra-backend

## Audio transcription wrapper for the llama-cpp backend.
## Drives the new AudioTranscription / AudioTranscriptionStream RPCs against
## ggml-org/Qwen3-ASR-0.6B-GGUF (a small ASR model that requires its mmproj
## audio encoder companion). The audio fixture is a short public-domain
## "jfk.wav" clip ggml-org bundles with whisper.cpp's CI assets.
test-extra-backend-llama-cpp-transcription: docker-build-llama-cpp
	BACKEND_IMAGE=local-ai-backend:llama-cpp \
	BACKEND_TEST_MODEL_URL=https://huggingface.co/ggml-org/Qwen3-ASR-0.6B-GGUF/resolve/main/Qwen3-ASR-0.6B-Q8_0.gguf \
	BACKEND_TEST_MMPROJ_URL=https://huggingface.co/ggml-org/Qwen3-ASR-0.6B-GGUF/resolve/main/mmproj-Qwen3-ASR-0.6B-Q8_0.gguf \
	BACKEND_TEST_AUDIO_URL=https://github.com/ggml-org/whisper.cpp/raw/master/samples/jfk.wav \
	BACKEND_TEST_CAPS=health,load,transcription \
	$(MAKE) test-extra-backend

## vllm is resolved from a HuggingFace model id (no file download) and
## exercises Predict + streaming + tool-call extraction via the hermes parser.
## Requires a host CPU with the SIMD instructions the prebuilt vllm CPU
## wheel was compiled against (AVX-512 VNNI/BF16); older CPUs will SIGILL
## on import — on CI this means using the bigger-runner label.
test-extra-backend-vllm: docker-build-vllm
	BACKEND_IMAGE=local-ai-backend:vllm \
	BACKEND_TEST_MODEL_NAME=Qwen/Qwen2.5-0.5B-Instruct \
	BACKEND_TEST_CAPS=health,load,predict,stream,tools \
	BACKEND_TEST_OPTIONS=tool_parser:hermes \
	$(MAKE) test-extra-backend

## tinygrad mirrors the vllm target (same model, same caps, same parser) so
## the two backends are directly comparable. The LLM path covers Predict,
## streaming and native tool-call extraction. Companion targets below cover
## embeddings, Stable Diffusion and Whisper — run them individually or via
## the `test-extra-backend-tinygrad-all` aggregate.
test-extra-backend-tinygrad: docker-build-tinygrad
	BACKEND_IMAGE=local-ai-backend:tinygrad \
	BACKEND_TEST_MODEL_NAME=Qwen/Qwen3-0.6B \
	BACKEND_TEST_CAPS=health,load,predict,stream,tools \
	BACKEND_TEST_OPTIONS=tool_parser:hermes \
	$(MAKE) test-extra-backend

## tinygrad — embeddings via LLM last-hidden-state pooling. Reuses the same
## Qwen3-0.6B as the chat target so we don't need a separate BERT vendor;
## the Embedding RPC mean-pools and L2-normalizes the last-layer hidden
## state.
test-extra-backend-tinygrad-embeddings: docker-build-tinygrad
	BACKEND_IMAGE=local-ai-backend:tinygrad \
	BACKEND_TEST_MODEL_NAME=Qwen/Qwen3-0.6B \
	BACKEND_TEST_CAPS=health,load,embeddings \
	$(MAKE) test-extra-backend

## tinygrad — Stable Diffusion 1.5. The original CompVis/runwayml repos have
## been gated, so we use the community-maintained mirror at
## stable-diffusion-v1-5/stable-diffusion-v1-5 with the EMA-only pruned
## checkpoint (~4.3GB). Step count is kept low (4) so a CPU-only run finishes
## in a few minutes; bump BACKEND_TEST_IMAGE_STEPS for higher quality.
test-extra-backend-tinygrad-sd: docker-build-tinygrad
	BACKEND_IMAGE=local-ai-backend:tinygrad \
	BACKEND_TEST_MODEL_NAME=stable-diffusion-v1-5/stable-diffusion-v1-5 \
	BACKEND_TEST_CAPS=health,load,image \
	$(MAKE) test-extra-backend

## tinygrad — Whisper. Loads OpenAI's tiny.en checkpoint (smallest at ~75MB)
## from the original azure CDN through tinygrad's `fetch` helper, and
## transcribes the canonical jfk.wav fixture from whisper.cpp's CI samples.
## Exercises both AudioTranscription and AudioTranscriptionStream.
test-extra-backend-tinygrad-whisper: docker-build-tinygrad
	BACKEND_IMAGE=local-ai-backend:tinygrad \
	BACKEND_TEST_MODEL_NAME=openai/whisper-tiny.en \
	BACKEND_TEST_AUDIO_URL=https://github.com/ggml-org/whisper.cpp/raw/master/samples/jfk.wav \
	BACKEND_TEST_CAPS=health,load,transcription \
	$(MAKE) test-extra-backend

test-extra-backend-tinygrad-all: \
	test-extra-backend-tinygrad \
	test-extra-backend-tinygrad-embeddings \
	test-extra-backend-tinygrad-sd \
	test-extra-backend-tinygrad-whisper

## insightface — face recognition.
##
## Face fixtures default to the sample images shipped in the
## deepinsight/insightface repository (MIT-licensed). For offline/local
## runs override with BACKEND_TEST_FACE_IMAGE_{1,2,3}_FILE pointing at
## local paths.
FACE_IMAGE_1_URL ?= https://github.com/deepinsight/insightface/raw/master/python-package/insightface/data/images/t1.jpg
FACE_IMAGE_2_URL ?= https://github.com/deepinsight/insightface/raw/master/python-package/insightface/data/images/t1.jpg
FACE_IMAGE_3_URL ?= https://github.com/deepinsight/insightface/raw/master/python-package/insightface/data/images/mask_white.jpg
## Known spoof fixture used by the face_antispoof e2e cap. This is
## upstream's own `image_F2.jpg` (Silent-Face repo, via yakhyo mirror)
## — verified to classify as is_real=false with score < 0.05 on the
## MiniFASNetV2 + MiniFASNetV1SE ensemble.
FACE_SPOOF_IMAGE_URL ?= https://github.com/yakhyo/face-anti-spoofing/raw/main/assets/image_F2.jpg

## Host-side cache for the OpenCV Zoo face ONNX files used by the
## opencv e2e target. The backend image no longer bakes model weights —
## gallery installs bring them via `files:` — but the e2e suite drives
## LoadModel over gRPC directly without going through the gallery. We
## pre-download the ONNX files to a stable host path and pass absolute
## paths in BACKEND_TEST_OPTIONS; `make` skips the downloads when the
## SHA-256 already matches.
INSIGHTFACE_OPENCV_DIR := /tmp/localai-insightface-opencv-cache
INSIGHTFACE_OPENCV_YUNET_URL := https://github.com/opencv/opencv_zoo/raw/main/models/face_detection_yunet/face_detection_yunet_2023mar.onnx
INSIGHTFACE_OPENCV_SFACE_URL := https://github.com/opencv/opencv_zoo/raw/main/models/face_recognition_sface/face_recognition_sface_2021dec.onnx
INSIGHTFACE_OPENCV_YUNET_SHA := 8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4
INSIGHTFACE_OPENCV_SFACE_SHA := 0ba9fbfa01b5270c96627c4ef784da859931e02f04419c829e83484087c34e79

## buffalo_sc (insightface) — pack zip + SHA-256 mirrors the gallery
## entry so the e2e target matches exactly what `local-ai models install
## insightface-buffalo-sc` would have fetched. Smallest insightface pack
## (~16MB) — keeps CI fast while still covering the insightface engine
## code path end-to-end.
INSIGHTFACE_BUFFALO_SC_DIR := /tmp/localai-insightface-buffalo-sc-cache
INSIGHTFACE_BUFFALO_SC_URL := https://github.com/deepinsight/insightface/releases/download/v0.7/buffalo_sc.zip
INSIGHTFACE_BUFFALO_SC_SHA := 57d31b56b6ffa911c8a73cfc1707c73cab76efe7f13b675a05223bf42de47c72

## Silent-Face antispoofing (MiniFASNetV2 + MiniFASNetV1SE) — shared
## between the buffalo_sc and opencv e2e targets. Both ONNX files are
## ~1.7MB, Apache 2.0. URLs + SHAs mirror the gallery entries.
INSIGHTFACE_ANTISPOOF_DIR := /tmp/localai-insightface-antispoof-cache
INSIGHTFACE_ANTISPOOF_V2_URL := https://github.com/yakhyo/face-anti-spoofing/releases/download/weights/MiniFASNetV2.onnx
INSIGHTFACE_ANTISPOOF_V2_SHA := b32929adc2d9c34b9486f8c4c7bc97c1b69bc0ea9befefc380e4faae4e463907
INSIGHTFACE_ANTISPOOF_V1SE_URL := https://github.com/yakhyo/face-anti-spoofing/releases/download/weights/MiniFASNetV1SE.onnx
INSIGHTFACE_ANTISPOOF_V1SE_SHA := ebab7f90c7833fbccd46d3a555410e78d969db5438e169b6524be444862b3676

.PHONY: insightface-opencv-models
insightface-opencv-models:
	@mkdir -p $(INSIGHTFACE_OPENCV_DIR)
	@if [ "$$(sha256sum $(INSIGHTFACE_OPENCV_DIR)/yunet.onnx 2>/dev/null | awk '{print $$1}')" != "$(INSIGHTFACE_OPENCV_YUNET_SHA)" ]; then \
		echo "Fetching YuNet..."; \
		curl -fsSL -o $(INSIGHTFACE_OPENCV_DIR)/yunet.onnx $(INSIGHTFACE_OPENCV_YUNET_URL); \
		echo "$(INSIGHTFACE_OPENCV_YUNET_SHA)  $(INSIGHTFACE_OPENCV_DIR)/yunet.onnx" | sha256sum -c; \
	fi
	@if [ "$$(sha256sum $(INSIGHTFACE_OPENCV_DIR)/sface.onnx 2>/dev/null | awk '{print $$1}')" != "$(INSIGHTFACE_OPENCV_SFACE_SHA)" ]; then \
		echo "Fetching SFace..."; \
		curl -fsSL -o $(INSIGHTFACE_OPENCV_DIR)/sface.onnx $(INSIGHTFACE_OPENCV_SFACE_URL); \
		echo "$(INSIGHTFACE_OPENCV_SFACE_SHA)  $(INSIGHTFACE_OPENCV_DIR)/sface.onnx" | sha256sum -c; \
	fi

.PHONY: insightface-antispoof-models
insightface-antispoof-models:
	@mkdir -p $(INSIGHTFACE_ANTISPOOF_DIR)
	@if [ "$$(sha256sum $(INSIGHTFACE_ANTISPOOF_DIR)/MiniFASNetV2.onnx 2>/dev/null | awk '{print $$1}')" != "$(INSIGHTFACE_ANTISPOOF_V2_SHA)" ]; then \
		echo "Fetching MiniFASNetV2..."; \
		curl -fsSL -o $(INSIGHTFACE_ANTISPOOF_DIR)/MiniFASNetV2.onnx $(INSIGHTFACE_ANTISPOOF_V2_URL); \
		echo "$(INSIGHTFACE_ANTISPOOF_V2_SHA)  $(INSIGHTFACE_ANTISPOOF_DIR)/MiniFASNetV2.onnx" | sha256sum -c; \
	fi
	@if [ "$$(sha256sum $(INSIGHTFACE_ANTISPOOF_DIR)/MiniFASNetV1SE.onnx 2>/dev/null | awk '{print $$1}')" != "$(INSIGHTFACE_ANTISPOOF_V1SE_SHA)" ]; then \
		echo "Fetching MiniFASNetV1SE..."; \
		curl -fsSL -o $(INSIGHTFACE_ANTISPOOF_DIR)/MiniFASNetV1SE.onnx $(INSIGHTFACE_ANTISPOOF_V1SE_URL); \
		echo "$(INSIGHTFACE_ANTISPOOF_V1SE_SHA)  $(INSIGHTFACE_ANTISPOOF_DIR)/MiniFASNetV1SE.onnx" | sha256sum -c; \
	fi

.PHONY: insightface-buffalo-sc-models
insightface-buffalo-sc-models:
	@mkdir -p $(INSIGHTFACE_BUFFALO_SC_DIR)
	@if [ "$$(sha256sum $(INSIGHTFACE_BUFFALO_SC_DIR)/buffalo_sc.zip 2>/dev/null | awk '{print $$1}')" != "$(INSIGHTFACE_BUFFALO_SC_SHA)" ]; then \
		echo "Fetching buffalo_sc..."; \
		curl -fsSL -o $(INSIGHTFACE_BUFFALO_SC_DIR)/buffalo_sc.zip $(INSIGHTFACE_BUFFALO_SC_URL); \
		echo "$(INSIGHTFACE_BUFFALO_SC_SHA)  $(INSIGHTFACE_BUFFALO_SC_DIR)/buffalo_sc.zip" | sha256sum -c; \
		rm -f $(INSIGHTFACE_BUFFALO_SC_DIR)/*.onnx; \
	fi
	@if [ ! -f "$(INSIGHTFACE_BUFFALO_SC_DIR)/det_500m.onnx" ]; then \
		echo "Extracting buffalo_sc..."; \
		unzip -o -q $(INSIGHTFACE_BUFFALO_SC_DIR)/buffalo_sc.zip -d $(INSIGHTFACE_BUFFALO_SC_DIR); \
	fi

## buffalo_sc — smallest insightface pack (SCRFD-500MF detector + MBF
## recognizer, ~16MB). Exercises the insightface engine code path
## (model_zoo-backed inference) without the ~326MB buffalo_l download.
## No age/gender/landmark heads — face_analyze is dropped from caps.
## The pack is pre-fetched on the host and passed as `root:<dir>` since
## the e2e suite drives LoadModel directly without going through
## LocalAI's gallery flow (which is what would normally populate
## ModelPath and in turn the engine's `_model_dir` option).
test-extra-backend-insightface-buffalo-sc: docker-build-insightface insightface-buffalo-sc-models insightface-antispoof-models
	BACKEND_IMAGE=local-ai-backend:insightface \
	BACKEND_TEST_MODEL_NAME=insightface-buffalo-sc \
	BACKEND_TEST_OPTIONS=engine:insightface,model_pack:buffalo_sc,root:$(INSIGHTFACE_BUFFALO_SC_DIR),antispoof_v2_onnx:$(INSIGHTFACE_ANTISPOOF_DIR)/MiniFASNetV2.onnx,antispoof_v1se_onnx:$(INSIGHTFACE_ANTISPOOF_DIR)/MiniFASNetV1SE.onnx \
	BACKEND_TEST_CAPS=health,load,face_detect,face_embed,face_verify,face_antispoof \
	BACKEND_TEST_FACE_IMAGE_1_URL=$(FACE_IMAGE_1_URL) \
	BACKEND_TEST_FACE_IMAGE_2_URL=$(FACE_IMAGE_2_URL) \
	BACKEND_TEST_FACE_IMAGE_3_URL=$(FACE_IMAGE_3_URL) \
	BACKEND_TEST_FACE_SPOOF_IMAGE_URL=$(FACE_SPOOF_IMAGE_URL) \
	BACKEND_TEST_VERIFY_DISTANCE_CEILING=0.55 \
	$(MAKE) test-extra-backend

## OpenCV Zoo YuNet + SFace — Apache 2.0, commercial-safe. face_analyze
## cap is dropped (SFace has no demographic head). The ONNX files are
## pre-fetched on the host via the insightface-opencv-models target and
## passed as absolute paths, since the e2e suite drives LoadModel
## directly without going through LocalAI's gallery flow.
test-extra-backend-insightface-opencv: docker-build-insightface insightface-opencv-models insightface-antispoof-models
	BACKEND_IMAGE=local-ai-backend:insightface \
	BACKEND_TEST_MODEL_NAME=insightface-opencv \
	BACKEND_TEST_OPTIONS=engine:onnx_direct,detector_onnx:$(INSIGHTFACE_OPENCV_DIR)/yunet.onnx,recognizer_onnx:$(INSIGHTFACE_OPENCV_DIR)/sface.onnx,antispoof_v2_onnx:$(INSIGHTFACE_ANTISPOOF_DIR)/MiniFASNetV2.onnx,antispoof_v1se_onnx:$(INSIGHTFACE_ANTISPOOF_DIR)/MiniFASNetV1SE.onnx \
	BACKEND_TEST_CAPS=health,load,face_detect,face_embed,face_verify,face_antispoof \
	BACKEND_TEST_FACE_IMAGE_1_URL=$(FACE_IMAGE_1_URL) \
	BACKEND_TEST_FACE_IMAGE_2_URL=$(FACE_IMAGE_2_URL) \
	BACKEND_TEST_FACE_IMAGE_3_URL=$(FACE_IMAGE_3_URL) \
	BACKEND_TEST_FACE_SPOOF_IMAGE_URL=$(FACE_SPOOF_IMAGE_URL) \
	BACKEND_TEST_VERIFY_DISTANCE_CEILING=0.55 \
	$(MAKE) test-extra-backend

## Aggregate — runs both face-recognition model configurations so CI
## catches regressions across engines together.
test-extra-backend-insightface-all: \
	test-extra-backend-insightface-buffalo-sc \
	test-extra-backend-insightface-opencv

## speaker-recognition — voice (speaker) biometrics.
##
## Audio fixtures default to the speechbrain test samples served
## straight from their GitHub repo — public, no auth needed, and they
## ship as 16kHz mono WAV/FLAC which is exactly what the engine wants.
## example{1,2,5} are three different speakers; the suite treats
## example1 as the "same-image twin" probe (verify(clip, clip) must
## return distance≈0) and the other two as cross-speaker ceilings.
## Override with BACKEND_TEST_VOICE_AUDIO_{1,2,3}_FILE for offline runs.
VOICE_AUDIO_1_URL ?= https://github.com/speechbrain/speechbrain/raw/develop/tests/samples/single-mic/example1.wav
VOICE_AUDIO_2_URL ?= https://github.com/speechbrain/speechbrain/raw/develop/tests/samples/single-mic/example2.flac
VOICE_AUDIO_3_URL ?= https://github.com/speechbrain/speechbrain/raw/develop/tests/samples/single-mic/example5.wav

## ECAPA-TDNN via SpeechBrain — default CI configuration. Auto-downloads
## the checkpoint from HuggingFace on first LoadModel (bundled in the
## backend image pip install). 192-d embeddings, cosine-distance based.
## The e2e suite drives LoadModel directly so we don't rely on LocalAI's
## gallery flow here.
test-extra-backend-speaker-recognition-ecapa: docker-build-speaker-recognition
	BACKEND_IMAGE=local-ai-backend:speaker-recognition \
	BACKEND_TEST_MODEL_NAME=speechbrain/spkrec-ecapa-voxceleb \
	BACKEND_TEST_OPTIONS=engine:speechbrain,source:speechbrain/spkrec-ecapa-voxceleb \
	BACKEND_TEST_CAPS=health,load,voice_embed,voice_verify \
	BACKEND_TEST_VOICE_AUDIO_1_URL=$(VOICE_AUDIO_1_URL) \
	BACKEND_TEST_VOICE_AUDIO_2_URL=$(VOICE_AUDIO_2_URL) \
	BACKEND_TEST_VOICE_AUDIO_3_URL=$(VOICE_AUDIO_3_URL) \
	BACKEND_TEST_VOICE_VERIFY_DISTANCE_CEILING=0.4 \
	$(MAKE) test-extra-backend

## Aggregate — today there's only one voice config; the target exists
## so the CI workflow matches the insightface-all naming convention and
## can grow to include WeSpeaker / 3D-Speaker later.
test-extra-backend-speaker-recognition-all: \
	test-extra-backend-speaker-recognition-ecapa

## Realtime e2e with sherpa-onnx driving VAD + STT + TTS against a mocked
## LLM. Extracts the sherpa-onnx Docker image rootfs, downloads the three
## gallery-referenced model bundles (silero-vad, omnilingual-asr, vits-ljs),
## writes the corresponding model config YAMLs, and runs the realtime
## websocket spec in tests/e2e with REALTIME_* env vars wiring the sherpa
## slots into the pipeline. The LLM slot stays on the in-repo mock-backend
## registered unconditionally by tests/e2e/e2e_suite_test.go. See
## tests/e2e/run-realtime-sherpa.sh for the full orchestration.
test-extra-e2e-realtime-sherpa: build-mock-backend docker-build-sherpa-onnx protogen-go react-ui
	bash tests/e2e/run-realtime-sherpa.sh

## Streaming ASR via the sherpa-onnx online recognizer. Uses the streaming
## zipformer English model (encoder/decoder/joiner int8 + tokens) from the
## sherpa-onnx gallery entry. Drives both AudioTranscription and
## AudioTranscriptionStream via the e2e-backends gRPC harness; streaming
## emits real partial deltas during decode. Each file is renamed on download
## to the shape sherpa-onnx's online loader expects (encoder.int8.onnx etc.).
test-extra-backend-sherpa-onnx-transcription: docker-build-sherpa-onnx
	BACKEND_IMAGE=local-ai-backend:sherpa-onnx \
	BACKEND_TEST_MODEL_URL='https://huggingface.co/csukuangfj/sherpa-onnx-streaming-zipformer-en-2023-06-26/resolve/main/encoder-epoch-99-avg-1-chunk-16-left-128.int8.onnx#encoder.int8.onnx' \
	BACKEND_TEST_EXTRA_FILES='https://huggingface.co/csukuangfj/sherpa-onnx-streaming-zipformer-en-2023-06-26/resolve/main/decoder-epoch-99-avg-1-chunk-16-left-128.int8.onnx#decoder.int8.onnx|https://huggingface.co/csukuangfj/sherpa-onnx-streaming-zipformer-en-2023-06-26/resolve/main/joiner-epoch-99-avg-1-chunk-16-left-128.int8.onnx#joiner.int8.onnx|https://huggingface.co/csukuangfj/sherpa-onnx-streaming-zipformer-en-2023-06-26/resolve/main/tokens.txt' \
	BACKEND_TEST_AUDIO_URL=https://github.com/ggml-org/whisper.cpp/raw/master/samples/jfk.wav \
	BACKEND_TEST_CAPS=health,load,transcription \
	BACKEND_TEST_OPTIONS=subtype=online \
	$(MAKE) test-extra-backend

## VITS TTS via the sherpa-onnx backend. Pulls the individual files from
## HuggingFace (the vits-ljs release tarball lives on the k2-fsa github
## but is also mirrored as discrete files on HF). Exercises both
## TTS (write-to-file) and TTSStream (PCM chunks + WAV header) via the
## e2e-backends gRPC harness.
test-extra-backend-sherpa-onnx-tts: docker-build-sherpa-onnx
	BACKEND_IMAGE=local-ai-backend:sherpa-onnx \
	BACKEND_TEST_MODEL_URL='https://huggingface.co/csukuangfj/vits-ljs/resolve/main/vits-ljs.onnx#vits-ljs.onnx' \
	BACKEND_TEST_EXTRA_FILES='https://huggingface.co/csukuangfj/vits-ljs/resolve/main/tokens.txt|https://huggingface.co/csukuangfj/vits-ljs/resolve/main/lexicon.txt' \
	BACKEND_TEST_CAPS=health,load,tts \
	$(MAKE) test-extra-backend

## VibeVoice TTS via the vibevoice-cpp backend. ModelFile is the
## realtime gguf; the supplementary tokenizer + voice prompt land
## alongside it under the harness's models dir and are wired through
## via the standard Options[] convention (tokenizer=, voice=).
test-extra-backend-vibevoice-cpp-tts: docker-build-vibevoice-cpp
	BACKEND_IMAGE=local-ai-backend:vibevoice-cpp \
	BACKEND_TEST_MODEL_URL='https://huggingface.co/mudler/vibevoice.cpp-models/resolve/main/vibevoice-realtime-0.5B-q8_0.gguf#vibevoice-realtime-0.5B-q8_0.gguf' \
	BACKEND_TEST_EXTRA_FILES='https://huggingface.co/mudler/vibevoice.cpp-models/resolve/main/tokenizer.gguf#tokenizer.gguf|https://huggingface.co/mudler/vibevoice.cpp-models/resolve/main/voice-en-Carter_man.gguf#voice-en-Carter_man.gguf' \
	BACKEND_TEST_OPTIONS=tokenizer:tokenizer.gguf,voice:voice-en-Carter_man.gguf \
	BACKEND_TEST_CAPS=health,load,tts \
	$(MAKE) test-extra-backend

## VibeVoice ASR (long-form, with diarization). type=asr tells the
## backend's Load() to slot ModelFile into the asr_model role; the
## tokenizer is supplied via Options[]. Uses the Q4_K quant (~10 GB)
## rather than Q8_0 (~14 GB) so the bundle fits inside ubuntu-latest's
## post-image disk budget.
test-extra-backend-vibevoice-cpp-transcription: docker-build-vibevoice-cpp
	BACKEND_IMAGE=local-ai-backend:vibevoice-cpp \
	BACKEND_TEST_MODEL_URL='https://huggingface.co/mudler/vibevoice.cpp-models/resolve/main/vibevoice-asr-q4_k.gguf#vibevoice-asr-q4_k.gguf' \
	BACKEND_TEST_EXTRA_FILES='https://huggingface.co/mudler/vibevoice.cpp-models/resolve/main/tokenizer.gguf#tokenizer.gguf' \
	BACKEND_TEST_AUDIO_URL=https://github.com/ggml-org/whisper.cpp/raw/master/samples/jfk.wav \
	BACKEND_TEST_OPTIONS=type:asr,tokenizer:tokenizer.gguf \
	BACKEND_TEST_CAPS=health,load,transcription \
	$(MAKE) test-extra-backend

## sglang mirrors the vllm setup: HuggingFace model id, same tiny Qwen,
## tool-call extraction via sglang's native qwen parser. CPU builds use
## sglang's upstream pyproject_cpu.toml recipe (see backend/python/sglang/install.sh).
test-extra-backend-sglang: docker-build-sglang
	BACKEND_IMAGE=local-ai-backend:sglang \
	BACKEND_TEST_MODEL_NAME=Qwen/Qwen2.5-0.5B-Instruct \
	BACKEND_TEST_CAPS=health,load,predict,stream,tools \
	BACKEND_TEST_OPTIONS=tool_parser:qwen \
	$(MAKE) test-extra-backend


## mlx is Apple-Silicon-first — the MLX backend auto-detects the right tool
## parser from the chat template, so no tool_parser: option is needed (it
## would be ignored at runtime). Run this on macOS / arm64 with Metal; the
## Linux/CPU mlx variant is untested in CI.
test-extra-backend-mlx: docker-build-mlx
	BACKEND_IMAGE=local-ai-backend:mlx \
	BACKEND_TEST_MODEL_NAME=mlx-community/Qwen2.5-0.5B-Instruct-4bit \
	BACKEND_TEST_CAPS=health,load,predict,stream,tools \
	$(MAKE) test-extra-backend

test-extra-backend-mlx-vlm: docker-build-mlx-vlm
	BACKEND_IMAGE=local-ai-backend:mlx-vlm \
	BACKEND_TEST_MODEL_NAME=mlx-community/Qwen2.5-0.5B-Instruct-4bit \
	BACKEND_TEST_CAPS=health,load,predict,stream,tools \
	$(MAKE) test-extra-backend

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
		--build-arg BASE_IMAGE=intel/oneapi-basekit:2025.3.2-0-devel-ubuntu24.04 \
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
# ik-llama-cpp is a fork of llama.cpp with superior CPU performance
BACKEND_IK_LLAMA_CPP = ik-llama-cpp|ik-llama-cpp|.|false|false
# turboquant is a llama.cpp fork with TurboQuant KV-cache quantization.
# Reuses backend/cpp/llama-cpp grpc-server sources via a thin wrapper Makefile.
BACKEND_TURBOQUANT = turboquant|turboquant|.|false|false

# Golang backends
BACKEND_PIPER = piper|golang|.|false|true
BACKEND_LOCAL_STORE = local-store|golang|.|false|true
BACKEND_HUGGINGFACE = huggingface|golang|.|false|true
BACKEND_SILERO_VAD = silero-vad|golang|.|false|true
BACKEND_STABLEDIFFUSION_GGML = stablediffusion-ggml|golang|.|--progress=plain|true
BACKEND_WHISPER = whisper|golang|.|false|true
BACKEND_VOXTRAL = voxtral|golang|.|false|true
BACKEND_ACESTEP_CPP = acestep-cpp|golang|.|false|true
BACKEND_QWEN3_TTS_CPP = qwen3-tts-cpp|golang|.|false|true
BACKEND_VIBEVOICE_CPP = vibevoice-cpp|golang|.|false|true
BACKEND_OPUS = opus|golang|.|false|true
BACKEND_SHERPA_ONNX = sherpa-onnx|golang|.|false|true

# Python backends with root context
BACKEND_RERANKERS = rerankers|python|.|false|true
BACKEND_TRANSFORMERS = transformers|python|.|false|true
BACKEND_OUTETTS = outetts|python|.|false|true
BACKEND_FASTER_WHISPER = faster-whisper|python|.|false|true
BACKEND_COQUI = coqui|python|.|false|true
BACKEND_RFDETR = rfdetr|python|.|false|true
BACKEND_INSIGHTFACE = insightface|python|.|false|true
BACKEND_SPEAKER_RECOGNITION = speaker-recognition|python|.|false|true
BACKEND_KITTEN_TTS = kitten-tts|python|.|false|true
BACKEND_NEUTTS = neutts|python|.|false|true
BACKEND_KOKORO = kokoro|python|.|false|true
BACKEND_VLLM = vllm|python|.|false|true
BACKEND_VLLM_OMNI = vllm-omni|python|.|false|true
BACKEND_SGLANG = sglang|python|.|false|true
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
BACKEND_MLX = mlx|python|.|false|true
BACKEND_MLX_VLM = mlx-vlm|python|.|false|true
BACKEND_MLX_DISTRIBUTED = mlx-distributed|python|./|false|true
BACKEND_TRL = trl|python|.|false|true
BACKEND_LLAMA_CPP_QUANTIZATION = llama-cpp-quantization|python|.|false|true
BACKEND_TINYGRAD = tinygrad|python|.|false|true

# Rust backends
BACKEND_KOKOROS = kokoros|rust|.|false|true

# C++ backends (Go wrapper with purego)
BACKEND_SAM3_CPP = sam3-cpp|golang|.|false|true

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
		$(if $(FROM_SOURCE),--build-arg FROM_SOURCE=$(FROM_SOURCE)) \
		$(if $(AMDGPU_TARGETS),--build-arg AMDGPU_TARGETS=$(AMDGPU_TARGETS)) \
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
$(eval $(call generate-docker-build-target,$(BACKEND_IK_LLAMA_CPP)))
$(eval $(call generate-docker-build-target,$(BACKEND_TURBOQUANT)))
$(eval $(call generate-docker-build-target,$(BACKEND_PIPER)))
$(eval $(call generate-docker-build-target,$(BACKEND_LOCAL_STORE)))
$(eval $(call generate-docker-build-target,$(BACKEND_HUGGINGFACE)))
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
$(eval $(call generate-docker-build-target,$(BACKEND_INSIGHTFACE)))
$(eval $(call generate-docker-build-target,$(BACKEND_SPEAKER_RECOGNITION)))
$(eval $(call generate-docker-build-target,$(BACKEND_KITTEN_TTS)))
$(eval $(call generate-docker-build-target,$(BACKEND_NEUTTS)))
$(eval $(call generate-docker-build-target,$(BACKEND_KOKORO)))
$(eval $(call generate-docker-build-target,$(BACKEND_VLLM)))
$(eval $(call generate-docker-build-target,$(BACKEND_VLLM_OMNI)))
$(eval $(call generate-docker-build-target,$(BACKEND_SGLANG)))
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
$(eval $(call generate-docker-build-target,$(BACKEND_QWEN3_TTS_CPP)))
$(eval $(call generate-docker-build-target,$(BACKEND_VIBEVOICE_CPP)))
$(eval $(call generate-docker-build-target,$(BACKEND_MLX)))
$(eval $(call generate-docker-build-target,$(BACKEND_MLX_VLM)))
$(eval $(call generate-docker-build-target,$(BACKEND_MLX_DISTRIBUTED)))
$(eval $(call generate-docker-build-target,$(BACKEND_TRL)))
$(eval $(call generate-docker-build-target,$(BACKEND_LLAMA_CPP_QUANTIZATION)))
$(eval $(call generate-docker-build-target,$(BACKEND_TINYGRAD)))
$(eval $(call generate-docker-build-target,$(BACKEND_KOKOROS)))
$(eval $(call generate-docker-build-target,$(BACKEND_SAM3_CPP)))
$(eval $(call generate-docker-build-target,$(BACKEND_SHERPA_ONNX)))

# Pattern rule for docker-save targets
docker-save-%: backend-images
	docker save local-ai-backend:$* -o backend-images/$*.tar

docker-build-backends: docker-build-llama-cpp docker-build-ik-llama-cpp docker-build-turboquant docker-build-rerankers docker-build-vllm docker-build-vllm-omni docker-build-sglang docker-build-transformers docker-build-outetts docker-build-diffusers docker-build-kokoro docker-build-faster-whisper docker-build-coqui docker-build-chatterbox docker-build-vibevoice docker-build-moonshine docker-build-pocket-tts docker-build-qwen-tts docker-build-fish-speech docker-build-faster-qwen3-tts docker-build-qwen-asr docker-build-nemo docker-build-voxcpm docker-build-whisperx docker-build-ace-step docker-build-acestep-cpp docker-build-voxtral docker-build-mlx-distributed docker-build-trl docker-build-llama-cpp-quantization docker-build-tinygrad docker-build-kokoros docker-build-sam3-cpp docker-build-qwen3-tts-cpp docker-build-vibevoice-cpp docker-build-insightface docker-build-speaker-recognition docker-build-sherpa-onnx

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
