.PHONY: vibevoice
vibevoice:
	bash install.sh

.PHONY: run
run: vibevoice
	@echo "Running vibevoice..."
	bash run.sh
	@echo "vibevoice run."

.PHONY: test
test: vibevoice
	@echo "Testing vibevoice..."
	bash test.sh
	@echo "vibevoice tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__