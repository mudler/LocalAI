.PHONY: sglang
sglang:
	bash install.sh

.PHONY: run
run: sglang
	@echo "Running sglang..."
	bash run.sh
	@echo "sglang run."

.PHONY: test
test: sglang
	@echo "Testing sglang..."
	bash test.sh
	@echo "sglang tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
