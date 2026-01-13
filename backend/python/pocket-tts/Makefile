.PHONY: pocket-tts
pocket-tts:
	bash install.sh

.PHONY: run
run: pocket-tts
	@echo "Running pocket-tts..."
	bash run.sh
	@echo "pocket-tts run."

.PHONY: test
test: pocket-tts
	@echo "Testing pocket-tts..."
	bash test.sh
	@echo "pocket-tts tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
