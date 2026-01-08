.PHONY: exllama2
exllama2:
	bash install.sh

.PHONY: run
run: exllama2
	@echo "Running exllama2..."
	bash run.sh
	@echo "exllama2 run."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	$(RM) -r venv source __pycache__