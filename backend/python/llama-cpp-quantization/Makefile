# Version of llama.cpp to fetch convert_hf_to_gguf.py from
LLAMA_CPP_CONVERT_VERSION ?= master

.PHONY: llama-cpp-quantization
llama-cpp-quantization:
	LLAMA_CPP_CONVERT_VERSION=$(LLAMA_CPP_CONVERT_VERSION) bash install.sh

.PHONY: run
run: llama-cpp-quantization
	@echo "Running llama-cpp-quantization..."
	bash run.sh
	@echo "llama-cpp-quantization run."

.PHONY: test
test: llama-cpp-quantization
	@echo "Testing llama-cpp-quantization..."
	bash test.sh
	@echo "llama-cpp-quantization tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
