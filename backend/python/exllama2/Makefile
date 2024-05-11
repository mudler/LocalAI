.PHONY: exllama2
exllama2: protogen
	bash install.sh

.PHONY: run
run: protogen
	@echo "Running exllama2..."
	bash run.sh
	@echo "exllama2 run."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto

.PHONY: clean
clean: protogen-clean
	$(RM) -r venv source __pycache__