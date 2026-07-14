.PHONY: voxcpm
voxcpm:
	bash install.sh

.PHONY: run
run: voxcpm
	@echo "Running voxcpm..."
	bash run.sh
	@echo "voxcpm run."

.PHONY: test
test: voxcpm
	@echo "Testing voxcpm..."
	bash test.sh
	@echo "voxcpm tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
