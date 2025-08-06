.PHONY: kokoro
kokoro: protogen
	bash install.sh

.PHONY: run
run: protogen
	@echo "Running kokoro..."
	bash run.sh
	@echo "kokoro run."

.PHONY: test
test: protogen
	@echo "Testing kokoro..."
	bash test.sh
	@echo "kokoro tested."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. -I./ --python_out=. --grpc_python_out=. backend.proto

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__