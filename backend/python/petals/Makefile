.PHONY: petals
petals: protogen
	@echo "Creating virtual environment..."
	bash install.sh "petals.yml"
	@echo "Virtual environment created."

.PHONY: run
run: protogen
	@echo "Running petals..."
	bash run.sh
	@echo "petals run."

.PHONY: test
test: protogen
	@echo "Testing petals..."
	bash test.sh
	@echo "petals tested."

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