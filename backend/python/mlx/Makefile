.PHONY: mlx
mlx:
	bash install.sh

.PHONY: run
run:
	@echo "Running mlx..."
	bash run.sh
	@echo "mlx run."

.PHONY: test
test:
	@echo "Testing mlx..."
	bash test.sh
	@echo "mlx tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__