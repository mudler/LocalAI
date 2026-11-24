.PHONY: nemo-asr
nemo-asr:
	bash install.sh

.PHONY: run
run: nemo-asr
	@echo "Running nemo-asr..."
	bash run.sh
	@echo "nemo-asr run."

.PHONY: test
test: nemo-asr
	@echo "Testing nemo-asr..."
	bash test.sh
	@echo "nemo-asr tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
