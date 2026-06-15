.PHONY: fish-speech
fish-speech:
	bash install.sh

.PHONY: run
run: fish-speech
	@echo "Running fish-speech..."
	bash run.sh
	@echo "fish-speech run."

.PHONY: test
test: fish-speech
	@echo "Testing fish-speech..."
	bash test.sh
	@echo "fish-speech tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
