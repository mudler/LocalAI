.PHONY: coqui
coqui: protogen
	$(MAKE) -C ../common-env/transformers

.PHONY: run
run: protogen
	@echo "Running coqui..."
	bash run.sh
	@echo "coqui run."

.PHONY: test
test: protogen
	@echo "Testing coqui..."
	bash test.sh
	@echo "coqui tested."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto