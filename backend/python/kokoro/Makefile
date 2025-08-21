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

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__