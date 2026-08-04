.PHONY: fish-speech
fish-speech:
	bash install.sh

.PHONY: run
run: fish-speech
	@echo "Running fish-speech..."
	bash run.sh
	@echo "fish-speech run."

.PHONY: test-unit
test-unit:
	python3 -m unittest -v prepare_upstream_test.py
	bash run_test.sh

.PHONY: test
test: fish-speech test-unit
	@echo "Testing fish-speech..."
	bash test.sh
	@echo "fish-speech tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
