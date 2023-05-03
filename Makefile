GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_NAME=local-ai
# renovate: datasource=github-tags depName=go-skynet/go-llama.cpp
GOLLAMA_VERSION?=llama.cpp-f4cef87
# renovate: datasource=git-refs packageNameTemplate=https://github.com/go-skynet/go-gpt4all-j.cpp currentValueTemplate=master depNameTemplate=go-gpt4all-j.cpp
GOGPT4ALLJ_VERSION?=1f7bff57f66cb7062e40d0ac3abd2217815e5109
# renovate: datasource=git-refs packageNameTemplate=https://github.com/go-skynet/go-gpt2.cpp currentValueTemplate=master depNameTemplate=go-gpt2.cpp
GOGPT2_VERSION?=245a5bfe6708ab80dc5c733dcdbfbe3cfd2acdaa

# here until https://github.com/donomii/go-rwkv.cpp/pull/1 is merged
RWKV_REPO?=https://github.com/mudler/go-rwkv.cpp
RWKV_VERSION?=6ba15255b03016b5ecce36529b500d21815399a7

GREEN  := $(shell tput -Txterm setaf 2)
YELLOW := $(shell tput -Txterm setaf 3)
WHITE  := $(shell tput -Txterm setaf 7)
CYAN   := $(shell tput -Txterm setaf 6)
RESET  := $(shell tput -Txterm sgr0)

C_INCLUDE_PATH=$(shell pwd)/go-llama:$(shell pwd)/go-gpt4all-j:$(shell pwd)/go-gpt2:$(shell pwd)/go-rwkv
LIBRARY_PATH=$(shell pwd)/go-llama:$(shell pwd)/go-gpt4all-j:$(shell pwd)/go-gpt2:$(shell pwd)/go-rwkv

# Use this if you want to set the default behavior
ifndef BUILD_TYPE
	BUILD_TYPE:=default
endif

ifeq ($(BUILD_TYPE), "generic")
	GENERIC_PREFIX:=generic-
else
	GENERIC_PREFIX:=
endif

.PHONY: all test build vendor

all: help

## GPT4ALL-J
go-gpt4all-j:
	git clone --recurse-submodules https://github.com/go-skynet/go-gpt4all-j.cpp go-gpt4all-j
	cd go-gpt4all-j && git checkout -b build $(GOGPT4ALLJ_VERSION) && git submodule update --init --recursive --depth 1
	# This is hackish, but needed as both go-llama and go-gpt4allj have their own version of ggml..
	@find ./go-gpt4all-j -type f -name "*.c" -exec sed -i'' -e 's/ggml_/ggml_gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/ggml_/ggml_gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.h" -exec sed -i'' -e 's/ggml_/ggml_gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/gpt_/gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.h" -exec sed -i'' -e 's/gpt_/gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/json_/json_gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/void replace/void json_gptj_replace/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/::replace/::json_gptj_replace/g' {} +

## RWKV
go-rwkv:
	git clone --recurse-submodules $(RWKV_REPO) go-rwkv
	cd go-rwkv && git checkout -b build $(RWKV_VERSION) && git submodule update --init --recursive --depth 1

go-rwkv/librwkv.a: go-rwkv
	cd go-rwkv && cd rwkv.cpp &&	cmake . -DRWKV_BUILD_SHARED_LIBRARY=OFF &&	cmake --build . && 	cp librwkv.a .. && cp ggml/src/libggml.a ..

go-gpt4all-j/libgptj.a: go-gpt4all-j
	$(MAKE) -C go-gpt4all-j $(GENERIC_PREFIX)libgptj.a

## CEREBRAS GPT
go-gpt2: 
	git clone --recurse-submodules https://github.com/go-skynet/go-gpt2.cpp go-gpt2
	cd go-gpt2 && git checkout -b build $(GOGPT2_VERSION) && git submodule update --init --recursive --depth 1
	# This is hackish, but needed as both go-llama and go-gpt4allj have their own version of ggml..
	@find ./go-gpt2 -type f -name "*.c" -exec sed -i'' -e 's/ggml_/ggml_gpt2_/g' {} +
	@find ./go-gpt2 -type f -name "*.cpp" -exec sed -i'' -e 's/ggml_/ggml_gpt2_/g' {} +
	@find ./go-gpt2 -type f -name "*.h" -exec sed -i'' -e 's/ggml_/ggml_gpt2_/g' {} +
	@find ./go-gpt2 -type f -name "*.cpp" -exec sed -i'' -e 's/gpt_/gpt2_/g' {} +
	@find ./go-gpt2 -type f -name "*.h" -exec sed -i'' -e 's/gpt_/gpt2_/g' {} +
	@find ./go-gpt2 -type f -name "*.cpp" -exec sed -i'' -e 's/json_/json_gpt2_/g' {} +

go-gpt2/libgpt2.a: go-gpt2
	$(MAKE) -C go-gpt2 $(GENERIC_PREFIX)libgpt2.a

go-llama:
	git clone -b $(GOLLAMA_VERSION) --recurse-submodules https://github.com/go-skynet/go-llama.cpp go-llama

go-llama/libbinding.a: go-llama 
	$(MAKE) -C go-llama $(GENERIC_PREFIX)libbinding.a

replace:
	$(GOCMD) mod edit -replace github.com/go-skynet/go-llama.cpp=$(shell pwd)/go-llama
	$(GOCMD) mod edit -replace github.com/go-skynet/go-gpt4all-j.cpp=$(shell pwd)/go-gpt4all-j
	$(GOCMD) mod edit -replace github.com/go-skynet/go-gpt2.cpp=$(shell pwd)/go-gpt2
	$(GOCMD) mod edit -replace github.com/donomii/go-rwkv.cpp=$(shell pwd)/go-rwkv

prepare-sources: go-llama go-gpt2 go-gpt4all-j go-rwkv
	$(GOCMD) mod download

## GENERIC
rebuild: ## Rebuilds the project
	$(MAKE) -C go-llama clean
	$(MAKE) -C go-gpt4all-j clean
	$(MAKE) -C go-gpt2 clean
	$(MAKE) -C go-rwkv clean
	$(MAKE) build

prepare: prepare-sources go-llama/libbinding.a go-gpt4all-j/libgptj.a go-gpt2/libgpt2.a go-rwkv/librwkv.a replace ## Prepares for building

clean: ## Remove build related file
	rm -fr ./go-llama
	rm -rf ./go-gpt4all-j
	rm -rf ./go-gpt2
	rm -rf ./go-rwkv
	rm -rf $(BINARY_NAME)

## Build:

build: prepare ## Build the project
	$(info ${GREEN}I local-ai build info:${RESET})
	$(info ${GREEN}I BUILD_TYPE: ${YELLOW}$(BUILD_TYPE)${RESET})
	C_INCLUDE_PATH=${C_INCLUDE_PATH} LIBRARY_PATH=${LIBRARY_PATH} $(GOCMD) build -o $(BINARY_NAME) ./

generic-build: ## Build the project using generic
	BUILD_TYPE="generic" $(MAKE) build

## Run
run: prepare ## run local-ai
	C_INCLUDE_PATH=${C_INCLUDE_PATH} LIBRARY_PATH=${LIBRARY_PATH} $(GOCMD) run ./main.go

test-models/testmodel:
	mkdir test-models
	wget https://huggingface.co/concedo/cerebras-111M-ggml/resolve/main/cerberas-111m-q4_0.bin -O test-models/testmodel
	cp tests/fixtures/* test-models

test: prepare test-models/testmodel
	cp tests/fixtures/* test-models
	@C_INCLUDE_PATH=${C_INCLUDE_PATH} LIBRARY_PATH=${LIBRARY_PATH} CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models $(GOCMD) test -v -timeout 30m ./...

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
