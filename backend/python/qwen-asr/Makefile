.PHONY: qwen-asr
qwen-asr:
	bash install.sh

.PHONY: run
run: qwen-asr
	@echo "Running qwen-asr..."
	bash run.sh
	@echo "qwen-asr run."

.PHONY: test
test: qwen-asr
	@echo "Testing qwen-asr..."
	bash test.sh
	@echo "qwen-asr tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
