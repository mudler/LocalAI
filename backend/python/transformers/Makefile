.PHONY: transformers
transformers:
	bash install.sh

.PHONY: run
run: transformers
	@echo "Running transformers..."
	bash run.sh
	@echo "transformers run."

# It is not working well by using command line. It only6 works with IDE like VSCode.
.PHONY: test
test: transformers
	@echo "Testing transformers..."
	bash test.sh
	@echo "transformers tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__