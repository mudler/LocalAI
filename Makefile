GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_NAME=local-ai
GOLLAMA_VERSION?=llama.cpp-5ecff35

GREEN  := $(shell tput -Txterm setaf 2)
YELLOW := $(shell tput -Txterm setaf 3)
WHITE  := $(shell tput -Txterm setaf 7)
CYAN   := $(shell tput -Txterm setaf 6)
RESET  := $(shell tput -Txterm sgr0)

.PHONY: all test build vendor

all: help

## Build:

build: prepare ## Build the project
	C_INCLUDE_PATH=$(shell pwd)/go-llama.cpp:$(shell pwd)/go-gpt4all-j:$(shell pwd)/go-gpt2.cpp LIBRARY_PATH=$(shell pwd)/go-llama.cpp:$(shell pwd)/go-gpt4all-j:$(shell pwd)/go-gpt2.cpp $(GOCMD) build -o $(BINARY_NAME) ./

buildgeneric: prepare-generic ## Build the project
	C_INCLUDE_PATH=$(shell pwd)/go-llama.cpp:$(shell pwd)/go-gpt4all-j:$(shell pwd)/go-gpt2.cpp LIBRARY_PATH=$(shell pwd)/go-llama.cpp:$(shell pwd)/go-gpt4all-j:$(shell pwd)/go-gpt2.cpp $(GOCMD) build -o $(BINARY_NAME) ./

## GPT4ALL-J
go-gpt4all-j:
	git clone --recurse-submodules https://github.com/go-skynet/go-gpt4all-j.cpp go-gpt4all-j
# This is hackish, but needed as both go-llama and go-gpt4allj have their own version of ggml..
	@find ./go-gpt4all-j -type f -name "*.c" -exec sed -i'' -e 's/ggml_/ggml_gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/ggml_/ggml_gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.h" -exec sed -i'' -e 's/ggml_/ggml_gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/gpt_/gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.h" -exec sed -i'' -e 's/gpt_/gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/json_/json_gptj_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/void replace/void json_gptj_replace/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/::replace/::json_gptj_replace/g' {} +

go-gpt4all-j/libgptj.a: go-gpt4all-j
	$(MAKE) -C go-gpt4all-j libgptj.a

go-gpt4all-j/libgptj.a-generic: go-gpt4all-j
	$(MAKE) -C go-gpt4all-j generic-libgptj.a

# CEREBRAS GPT
go-gpt2.cpp:
	git clone --recurse-submodules https://github.com/go-skynet/go-gpt2.cpp go-gpt2.cpp
# This is hackish, but needed as both go-llama and go-gpt4allj have their own version of ggml..
	@find ./go-gpt2.cpp -type f -name "*.c" -exec sed -i'' -e 's/ggml_/ggml_gpt2_/g' {} +
	@find ./go-gpt2.cpp -type f -name "*.cpp" -exec sed -i'' -e 's/ggml_/ggml_gpt2_/g' {} +
	@find ./go-gpt2.cpp -type f -name "*.h" -exec sed -i'' -e 's/ggml_/ggml_gpt2_/g' {} +
	@find ./go-gpt2.cpp -type f -name "*.cpp" -exec sed -i'' -e 's/gpt_/gpt2_/g' {} +
	@find ./go-gpt2.cpp -type f -name "*.h" -exec sed -i'' -e 's/gpt_/gpt2_/g' {} +
	@find ./go-gpt2.cpp -type f -name "*.cpp" -exec sed -i'' -e 's/json_/json_gpt2_/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/void replace/void json_gpt2_replace/g' {} +
	@find ./go-gpt4all-j -type f -name "*.cpp" -exec sed -i'' -e 's/::replace/::json_gpt2_replace/g' {} +

go-gpt2.cpp/libgpt2.a: go-gpt2.cpp
	$(MAKE) -C go-gpt2.cpp libgpt2.a

go-gpt2.cpp/libgpt2.a-generic: go-gpt2.cpp
	$(MAKE) -C go-gpt2.cpp generic-libgpt2.a

go-llama:
	git clone -b $(GOLLAMA_VERSION) --recurse-submodules https://github.com/go-skynet/go-llama.cpp go-llama
	$(MAKE) -C go-llama libbinding.a

go-llama-generic:
	git clone -b $(GOLLAMA_VERSION) --recurse-submodules https://github.com/go-skynet/go-llama.cpp go-llama
	$(MAKE) -C go-llama generic-libbinding.a

replace:
	$(GOCMD) mod edit -replace github.com/go-skynet/go-llama.cpp=$(shell pwd)/go-llama
	$(GOCMD) mod edit -replace github.com/go-skynet/go-gpt4all-j.cpp=$(shell pwd)/go-gpt4all-j
	$(GOCMD) mod edit -replace github.com/go-skynet/go-gpt2.cpp=$(shell pwd)/go-gpt2.cpp

prepare: go-llama go-gpt4all-j/libgptj.a go-gpt2.cpp/libgpt2.a replace

prepare-generic: go-llama-generic go-gpt4all-j/libgptj.a-generic go-gpt2.cpp/libgpt2.a-generic replace

clean: ## Remove build related file
	rm -fr ./go-llama
	rm -rf ./go-gpt4all-j
	rm -rf ./go-gpt2.cpp
	rm -rf $(BINARY_NAME)

## Run:
run: prepare
	$(GOCMD) run ./ api

## Test:
test: ## Run the tests of the project
	$(GOTEST) -v -race ./... $(OUTPUT_OPTIONS)

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
