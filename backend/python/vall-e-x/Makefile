ifneq (,$(findstring sycl,$(BUILD_TYPE)))
export SKIP_CONDA=1
endif

.PHONY: ttsvalle
ttsvalle: protogen
	bash install.sh

.PHONY: run
run: protogen
	@echo "Running ttsvalle..."
	bash run.sh
	@echo "ttsvalle run."

.PHONY: test
test: protogen
	@echo "Testing valle..."
	bash test.sh
	@echo "valle tested."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto

.PHONY: clean
clean: protogen-clean
	rm -rf source venv __pycache__