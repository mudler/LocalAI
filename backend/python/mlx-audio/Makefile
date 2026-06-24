.PHONY: mlx-audio
mlx-audio:
	bash install.sh

.PHONY: run
run: mlx-audio
	@echo "Running mlx-audio..."
	bash run.sh
	@echo "mlx run."

.PHONY: test
test: mlx-audio
	@echo "Testing mlx-audio..."
	bash test.sh
	@echo "mlx tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__