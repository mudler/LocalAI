.PHONY: spiritlm
spiritlm:
	bash install.sh

.PHONY: run
run: spiritlm
	@echo "Running spiritlm..."
	bash run.sh
	@echo "spiritlm run."

.PHONY: test
test: spiritlm
	@echo "Testing spiritlm..."
	bash test.sh
	@echo "spiritlm tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
