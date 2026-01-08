.PHONY: ttsbark
ttsbark:
	bash install.sh

.PHONY: run
run: ttsbark
	@echo "Running bark..."
	bash run.sh
	@echo "bark run."

.PHONY: test
test: ttsbark
	@echo "Testing bark..."
	bash test.sh
	@echo "bark tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__