.PHONY: mlx-vlm
mlx-vlm:
	bash install.sh

.PHONY: run
run: mlx-vlm
	@echo "Running mlx-vlm..."
	bash run.sh
	@echo "mlx run."

.PHONY: test
test: mlx-vlm
	@echo "Testing mlx-vlm..."
	bash test.sh
	@echo "mlx tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__