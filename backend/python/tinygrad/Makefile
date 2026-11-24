.DEFAULT_GOAL := install

.PHONY: install
install:
	bash install.sh

.PHONY: run
run: install
	@echo "Running tinygrad..."
	bash run.sh
	@echo "tinygrad run."

.PHONY: test
test: install
	@echo "Testing tinygrad..."
	bash test.sh
	@echo "tinygrad tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
