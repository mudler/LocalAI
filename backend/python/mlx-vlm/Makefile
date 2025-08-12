.PHONY: mlx
mlx: protogen
	bash install.sh

.PHONY: run
run: protogen
	@echo "Running mlx..."
	bash run.sh
	@echo "mlx run."

.PHONY: test
test: protogen
	@echo "Testing mlx..."
	bash test.sh
	@echo "mlx tested."

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