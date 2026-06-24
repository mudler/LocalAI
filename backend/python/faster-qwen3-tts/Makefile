.PHONY: faster-qwen3-tts
faster-qwen3-tts:
	bash install.sh

.PHONY: run
run: faster-qwen3-tts
	@echo "Running faster-qwen3-tts..."
	bash run.sh
	@echo "faster-qwen3-tts run."

.PHONY: test
test: faster-qwen3-tts
	@echo "Testing faster-qwen3-tts..."
	bash test.sh
	@echo "faster-qwen3-tts tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
