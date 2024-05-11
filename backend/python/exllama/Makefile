export CONDA_ENV_PATH = "exllama.yml"

.PHONY: exllama
exllama: protogen
	bash install.sh ${CONDA_ENV_PATH}

.PHONY: run
run: protogen
	@echo "Running exllama..."
	bash run.sh
	@echo "exllama run."

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