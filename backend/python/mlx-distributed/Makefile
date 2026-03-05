.PHONY: mlx-distributed
mlx-distributed:
	bash install.sh

.PHONY: run
run:
	@echo "Running mlx-distributed..."
	bash run.sh
	@echo "mlx-distributed run."

.PHONY: test
test:
	@echo "Testing mlx-distributed..."
	bash test.sh
	@echo "mlx-distributed tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
