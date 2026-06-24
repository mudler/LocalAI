.PHONY: neutts
neutts:
	bash install.sh

.PHONY: run
run: neutts
	@echo "Running neutts..."
	bash run.sh
	@echo "neutts run."

.PHONY: test
test: neutts
	@echo "Testing neutts..."
	bash test.sh
	@echo "neutts tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__