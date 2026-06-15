.PHONY: chatterbox
chatterbox:
	bash install.sh

.PHONY: run
run: chatterbox
	@echo "Running coqui..."
	bash run.sh
	@echo "coqui run."

.PHONY: test
test: chatterbox
	@echo "Testing coqui..."
	bash test.sh
	@echo "coqui tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__