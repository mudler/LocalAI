.PHONY: qwen-tts
qwen-tts:
	bash install.sh

.PHONY: run
run: qwen-tts
	@echo "Running qwen-tts..."
	bash run.sh
	@echo "qwen-tts run."

.PHONY: test
test: qwen-tts
	@echo "Testing qwen-tts..."
	bash test.sh
	@echo "qwen-tts tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
