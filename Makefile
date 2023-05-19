GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_NAME=local-ai

GOLLAMA_VERSION?=b7bbefbe0b84262e003387a605842bdd0d099300
GPT4ALL_REPO?=https://github.com/nomic-ai/gpt4all
GPT4ALL_VERSION?=213e033540fa3b68202bb12cf7f0134cfe6638aa
GOGPT2_VERSION?=7bff56f0224502c1c9ed6258d2a17e8084628827
RWKV_REPO?=https://github.com/donomii/go-rwkv.cpp
RWKV_VERSION?=07166da10cb2a9e8854395a4f210464dcea76e47
WHISPER_CPP_VERSION?=95b02d76b04d18e4ce37ed8353a1f0797f1717ea
BERT_VERSION?=cea1ed76a7f48ef386a8e369f6c82c48cdf2d551
BLOOMZ_VERSION?=e9366e82abdfe70565644fbfae9651976714efd1
BUILD_TYPE?=
CGO_LDFLAGS?=
CUDA_LIBPATH?=/usr/local/cuda/lib64/
STABLEDIFFUSION_VERSION?=c0748eca3642d58bcf9521108bcee46959c647dc
GO_TAGS?=

OPTIONAL_TARGETS?=

GREEN  := $(shell tput -Txterm setaf 2)
YELLOW := $(shell tput -Txterm setaf 3)
WHITE  := $(shell tput -Txterm setaf 7)
CYAN   := $(shell tput -Txterm setaf 6)
RESET  := $(shell tput -Txterm sgr0)

C_INCLUDE_PATH=$(shell pwd)/go-llama:$(shell pwd)/go-stable-diffusion/:$(shell pwd)/gpt4all/gpt4all-bindings/golang/:$(shell pwd)/go-gpt2:$(shell pwd)/go-rwkv:$(shell pwd)/whisper.cpp:$(shell pwd)/go-bert:$(shell pwd)/bloomz
LIBRARY_PATH=$(shell pwd)/go-llama:$(shell pwd)/go-stable-diffusion/:$(shell pwd)/gpt4all/gpt4all-bindings/golang/:$(shell pwd)/go-gpt2:$(shell pwd)/go-rwkv:$(shell pwd)/whisper.cpp:$(shell pwd)/go-bert:$(shell pwd)/bloomz

ifeq ($(BUILD_TYPE),openblas)
	CGO_LDFLAGS+=-lopenblas
endif

ifeq ($(BUILD_TYPE),cublas)
	CGO_LDFLAGS+=-lcublas -lcudart -L$(CUDA_LIBPATH)
	export LLAMA_CUBLAS=1
endif

ifeq ($(GO_TAGS),stablediffusion)
	OPTIONAL_TARGETS+=go-stable-diffusion/libstablediffusion.a
endif

.PHONY: all test build vendor

all: help

## GPT4ALL
gpt4all:
	git clone --recurse-submodules $(GPT4ALL_REPO) gpt4all
	cd gpt4all && git checkout -b build $(GPT4ALL_VERSION) && git submodule update --init --recursive --depth 1
	# This is hackish, but needed as both go-llama and go-gpt4allj have their own version of ggml..
	@find ./gpt4all -type f -name "*.c" -exec sed -i'' -e 's/ggml_/ggml_gptj_/g' {} +
	@find ./gpt4all -type f -name "*.cpp" -exec sed -i'' -e 's/ggml_/ggml_gptj_/g' {} +
	@find ./gpt4all -type f -name "*.h" -exec sed -i'' -e 's/ggml_/ggml_gptj_/g' {} +
	@find ./gpt4all -type f -name "*.cpp" -exec sed -i'' -e 's/gpt_/gptj_/g' {} +
	@find ./gpt4all -type f -name "*.h" -exec sed -i'' -e 's/gpt_/gptj_/g' {} +
	@find ./gpt4all -type f -name "*.h" -exec sed -i'' -e 's/set_console_color/set_gptj_console_color/g' {} +
	@find ./gpt4all -type f -name "*.cpp" -exec sed -i'' -e 's/set_console_color/set_gptj_console_color/g' {} +
	@find ./gpt4all -type f -name "*.cpp" -exec sed -i'' -e 's/llama_/gptjllama_/g' {} +
	@find ./gpt4all -type f -name "*.go" -exec sed -i'' -e 's/llama_/gptjllama_/g' {} +
	@find ./gpt4all -type f -name "*.h" -exec sed -i'' -e 's/llama_/gptjllama_/g' {} +
	@find ./gpt4all -type f -name "*.txt" -exec sed -i'' -e 's/llama_/gptjllama_/g' {} +
	@find ./gpt4all -type f -name "*.cpp" -exec sed -i'' -e 's/json_/json_gptj_/g' {} +
	@find ./gpt4all -type f -name "*.cpp" -exec sed -i'' -e 's/void replace/void json_gptj_replace/g' {} +
	@find ./gpt4all -type f -name "*.cpp" -exec sed -i'' -e 's/::replace/::json_gptj_replace/g' {} +
	mv ./gpt4all/gpt4all-backend/llama.cpp/llama_util.h ./gpt4all/gpt4all-backend/llama.cpp/gptjllama_util.h

## BERT embeddings
go-bert:
	git clone --recurse-submodules https://github.com/go-skynet/go-bert.cpp go-bert
	cd go-bert && git checkout -b build $(BERT_VERSION) && git submodule update --init --recursive --depth 1
	@find ./go-bert -type f -name "*.c" -exec sed -i'' -e 's/ggml_/ggml_bert_/g' {} +
	@find ./go-bert -type f -name "*.cpp" -exec sed -i'' -e 's/ggml_/ggml_bert_/g' {} +
	@find ./go-bert -type f -name "*.h" -exec sed -i'' -e 's/ggml_/ggml_bert_/g' {} +

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
	@find ./go-rwkv -type f -name "*.c" -exec sed -i'' -e 's/ggml_/ggml_rwkv_/g' {} +
	@find ./go-rwkv -type f -name "*.cpp" -exec sed -i'' -e 's/ggml_/ggml_rwkv_/g' {} +
	@find ./go-rwkv -type f -name "*.h" -exec sed -i'' -e 's/ggml_/ggml_rwkv_/g' {} +

go-rwkv/librwkv.a: go-rwkv
	cd go-rwkv && cd rwkv.cpp &&	cmake . -DRWKV_BUILD_SHARED_LIBRARY=OFF &&	cmake --build . && 	cp librwkv.a .. && cp ggml/src/libggml.a ..

## bloomz
bloomz:
	git clone --recurse-submodules https://github.com/go-skynet/bloomz.cpp bloomz
	@find ./bloomz -type f -name "*.c" -exec sed -i'' -e 's/ggml_/ggml_bloomz_/g' {} +
	@find ./bloomz -type f -name "*.cpp" -exec sed -i'' -e 's/ggml_/ggml_bloomz_/g' {} +
	@find ./bloomz -type f -name "*.h" -exec sed -i'' -e 's/ggml_/ggml_bloomz_/g' {} +
	@find ./bloomz -type f -name "*.cpp" -exec sed -i'' -e 's/gpt_/gpt_bloomz_/g' {} +
	@find ./bloomz -type f -name "*.h" -exec sed -i'' -e 's/gpt_/gpt_bloomz_/g' {} +

bloomz/libbloomz.a: bloomz
	cd bloomz && make libbloomz.a

go-bert/libgobert.a: go-bert
	$(MAKE) -C go-bert libgobert.a

gpt4all/gpt4all-bindings/golang/libgpt4all.a: gpt4all
	$(MAKE) -C gpt4all/gpt4all-bindings/golang/ libgpt4all.a

## CEREBRAS GPT
go-gpt2: 
	git clone --recurse-submodules https://github.com/go-skynet/go-gpt2.cpp go-gpt2
	cd go-gpt2 && git checkout -b build $(GOGPT2_VERSION) && git submodule update --init --recursive --depth 1
	# This is hackish, but needed as both go-llama and go-gpt4allj have their own version of ggml..
	@find ./go-gpt2 -type f -name "*.c" -exec sed -i'' -e 's/ggml_/ggml_gpt2_/g' {} +
	@find ./go-gpt2 -type f -name "*.cpp" -exec sed -i'' -e 's/ggml_/ggml_gpt2_/g' {} +
	@find ./go-gpt2 -type f -name "*.h" -exec sed -i'' -e 's/ggml_/ggml_gpt2_/g' {} +
	@find ./go-gpt2 -type f -name "*.cpp" -exec sed -i'' -e 's/gpt_print_usage/gpt2_print_usage/g' {} +
	@find ./go-gpt2 -type f -name "*.h" -exec sed -i'' -e 's/gpt_print_usage/gpt2_print_usage/g' {} +
	@find ./go-gpt2 -type f -name "*.cpp" -exec sed -i'' -e 's/gpt_params_parse/gpt2_params_parse/g' {} +
	@find ./go-gpt2 -type f -name "*.h" -exec sed -i'' -e 's/gpt_params_parse/gpt2_params_parse/g' {} +
	@find ./go-gpt2 -type f -name "*.cpp" -exec sed -i'' -e 's/gpt_random_prompt/gpt2_random_prompt/g' {} +
	@find ./go-gpt2 -type f -name "*.h" -exec sed -i'' -e 's/gpt_random_prompt/gpt2_random_prompt/g' {} +
	@find ./go-gpt2 -type f -name "*.cpp" -exec sed -i'' -e 's/json_/json_gpt2_/g' {} +

go-gpt2/libgpt2.a: go-gpt2
	$(MAKE) -C go-gpt2 libgpt2.a

whisper.cpp:
	git clone https://github.com/ggerganov/whisper.cpp.git
	cd whisper.cpp && git checkout -b build $(WHISPER_CPP_VERSION) && git submodule update --init --recursive --depth 1
	@find ./whisper.cpp -type f -name "*.c" -exec sed -i'' -e 's/ggml_/ggml_whisper_/g' {} +
	@find ./whisper.cpp -type f -name "*.cpp" -exec sed -i'' -e 's/ggml_/ggml_whisper_/g' {} +
	@find ./whisper.cpp -type f -name "*.h" -exec sed -i'' -e 's/ggml_/ggml_whisper_/g' {} +

whisper.cpp/libwhisper.a: whisper.cpp
	cd whisper.cpp && make libwhisper.a

go-llama:
	git clone --recurse-submodules https://github.com/go-skynet/go-llama.cpp go-llama
	cd go-llama && git checkout -b build $(GOLLAMA_VERSION) && git submodule update --init --recursive --depth 1

go-llama/libbinding.a: go-llama 
	$(MAKE) -C go-llama BUILD_TYPE=$(BUILD_TYPE) libbinding.a

replace:
	$(GOCMD) mod edit -replace github.com/go-skynet/go-llama.cpp=$(shell pwd)/go-llama
	$(GOCMD) mod edit -replace github.com/nomic-ai/gpt4all/gpt4all-bindings/golang=$(shell pwd)/gpt4all/gpt4all-bindings/golang
	$(GOCMD) mod edit -replace github.com/go-skynet/go-gpt2.cpp=$(shell pwd)/go-gpt2
	$(GOCMD) mod edit -replace github.com/donomii/go-rwkv.cpp=$(shell pwd)/go-rwkv
	$(GOCMD) mod edit -replace github.com/ggerganov/whisper.cpp=$(shell pwd)/whisper.cpp
	$(GOCMD) mod edit -replace github.com/go-skynet/go-bert.cpp=$(shell pwd)/go-bert
	$(GOCMD) mod edit -replace github.com/go-skynet/bloomz.cpp=$(shell pwd)/bloomz
	$(GOCMD) mod edit -replace github.com/mudler/go-stable-diffusion=$(shell pwd)/go-stable-diffusion

prepare-sources: go-llama go-gpt2 gpt4all go-rwkv whisper.cpp go-bert bloomz go-stable-diffusion replace
	$(GOCMD) mod download

## GENERIC
rebuild: ## Rebuilds the project
	$(MAKE) -C go-llama clean
	$(MAKE) -C gpt4all/gpt4all-bindings/golang/ clean
	$(MAKE) -C go-gpt2 clean
	$(MAKE) -C go-rwkv clean
	$(MAKE) -C whisper.cpp clean
	$(MAKE) -C go-stable-diffusion clean
	$(MAKE) -C go-bert clean
	$(MAKE) -C bloomz clean
	$(MAKE) build

prepare: prepare-sources gpt4all/gpt4all-bindings/golang/libgpt4all.a $(OPTIONAL_TARGETS) go-llama/libbinding.a go-bert/libgobert.a go-gpt2/libgpt2.a go-rwkv/librwkv.a whisper.cpp/libwhisper.a bloomz/libbloomz.a  ## Prepares for building

clean: ## Remove build related file
	rm -fr ./go-llama
	rm -rf ./gpt4all
	rm -rf ./go-stable-diffusion
	rm -rf ./go-gpt2
	rm -rf ./go-rwkv
	rm -rf ./go-bert
	rm -rf ./bloomz
	rm -rf ./whisper.cpp
	rm -rf $(BINARY_NAME)

## Build:

build: prepare ## Build the project
	$(info ${GREEN}I local-ai build info:${RESET})
	$(info ${GREEN}I BUILD_TYPE: ${YELLOW}$(BUILD_TYPE)${RESET})
	$(info ${GREEN}I GO_TAGS: ${YELLOW}$(GO_TAGS)${RESET})
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=${C_INCLUDE_PATH} LIBRARY_PATH=${LIBRARY_PATH} $(GOCMD) build -tags "$(GO_TAGS)" -x -o $(BINARY_NAME) ./

generic-build: ## Build the project using generic
	BUILD_TYPE="generic" $(MAKE) build

## Run
run: prepare ## run local-ai
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH=${C_INCLUDE_PATH} LIBRARY_PATH=${LIBRARY_PATH} $(GOCMD) run ./main.go

test-models/testmodel:
	mkdir test-models
	mkdir test-dir
	wget https://huggingface.co/concedo/cerebras-111M-ggml/resolve/main/cerberas-111m-q4_0.bin -O test-models/testmodel
	wget https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin -O test-models/whisper-en
	wget https://huggingface.co/skeskinen/ggml/resolve/main/all-MiniLM-L6-v2/ggml-model-q4_0.bin -O test-models/bert
	wget https://cdn.openai.com/whisper/draft-20220913a/micro-machines.wav -O test-dir/audio.wav
	wget https://huggingface.co/imxcstar/rwkv-4-raven-ggml/resolve/main/RWKV-4-Raven-1B5-v11-Eng99%25-Other1%25-20230425-ctx4096-16_Q4_2.bin -O test-models/rwkv
	wget https://raw.githubusercontent.com/saharNooby/rwkv.cpp/5eb8f09c146ea8124633ab041d9ea0b1f1db4459/rwkv/20B_tokenizer.json -O test-models/rwkv.tokenizer.json
	cp tests/models_fixtures/* test-models

test: prepare test-models/testmodel
	cp tests/models_fixtures/* test-models
	C_INCLUDE_PATH=${C_INCLUDE_PATH} LIBRARY_PATH=${LIBRARY_PATH} TEST_DIR=$(abspath ./)/test-dir/ FIXTURES=$(abspath ./)/tests/fixtures CONFIG_FILE=$(abspath ./)/test-models/config.yaml MODELS_PATH=$(abspath ./)/test-models $(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo -v -r ./api ./pkg

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
