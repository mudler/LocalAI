# Version of llama.cpp to fetch convert_hf_to_gguf.py from (for GGUF export)
LLAMA_CPP_CONVERT_VERSION ?= master

.PHONY: trl
trl:
	LLAMA_CPP_CONVERT_VERSION=$(LLAMA_CPP_CONVERT_VERSION) bash install.sh

.PHONY: run
run: trl
	@echo "Running trl..."
	bash run.sh
	@echo "trl run."

.PHONY: test
test: trl
	@echo "Testing trl..."
	bash test.sh
	@echo "trl tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
