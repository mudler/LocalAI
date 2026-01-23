.PHONY: vllm-omni
vllm-omni:
	bash install.sh

.PHONY: run
run: vllm-omni
	@echo "Running vllm-omni..."
	bash run.sh
	@echo "vllm-omni run."

.PHONY: test
test: vllm-omni
	@echo "Testing vllm-omni..."
	bash test.sh
	@echo "vllm-omni tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
