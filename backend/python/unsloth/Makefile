.PHONY: unsloth
unsloth:
	bash install.sh

.PHONY: run
run: unsloth
	@echo "Running unsloth..."
	bash run.sh
	@echo "unsloth run."

.PHONY: test
test: unsloth
	@echo "Testing unsloth..."
	bash test.sh
	@echo "unsloth tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
