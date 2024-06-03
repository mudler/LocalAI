.PHONY: transformers-musicgen
transformers-musicgen: protogen
	bash install.sh

.PHONY: run
run: protogen
	@echo "Running transformers..."
	bash run.sh
	@echo "transformers run."

.PHONY: test
test: protogen
	@echo "Testing transformers..."
	bash test.sh
	@echo "transformers tested."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__