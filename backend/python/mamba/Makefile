.PHONY: mamba
mamba: protogen
	bash install.sh 

.PHONY: run
run: protogen
	@echo "Running mamba..."
	bash run.sh
	@echo "mamba run."

.PHONY: test
test: protogen
	@echo "Testing mamba..."
	bash test.sh
	@echo "mamba tested."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto

.PHONY: clean
clean: protogen-clean
	$(RM) -r venv __pycache__