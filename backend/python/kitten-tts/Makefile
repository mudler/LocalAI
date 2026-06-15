.PHONY: kitten-tts
kitten-tts:
	bash install.sh

.PHONY: run
run: kitten-tts
	@echo "Running kitten-tts..."
	bash run.sh
	@echo "kitten-tts run."

.PHONY: test
test: kitten-tts
	@echo "Testing kitten-tts..."
	bash test.sh
	@echo "kitten-tts tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__