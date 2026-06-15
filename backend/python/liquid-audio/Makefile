.PHONY: liquid-audio
liquid-audio:
	bash install.sh

.PHONY: run
run: liquid-audio
	@echo "Running liquid-audio..."
	bash run.sh
	@echo "liquid-audio run."

.PHONY: test
test: liquid-audio
	@echo "Testing liquid-audio..."
	bash test.sh
	@echo "liquid-audio tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
