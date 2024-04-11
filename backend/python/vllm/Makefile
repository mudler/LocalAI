.PHONY: vllm
vllm: protogen
	$(MAKE) -C ../common-env/transformers

.PHONY: run
run: protogen
	@echo "Running vllm..."
	bash run.sh
	@echo "vllm run."

.PHONY: test
test: protogen
	@echo "Testing vllm..."
	bash test.sh
	@echo "vllm tested."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto