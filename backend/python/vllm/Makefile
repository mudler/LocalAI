.PHONY: vllm
vllm:
	bash install.sh

.PHONY: run
run: vllm
	@echo "Running vllm..."
	bash run.sh
	@echo "vllm run."

.PHONY: test
test: vllm
	@echo "Testing vllm..."
	bash test.sh
	@echo "vllm tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__