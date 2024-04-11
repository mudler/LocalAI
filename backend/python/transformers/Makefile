.PHONY: transformers
transformers: protogen
	$(MAKE) -C ../common-env/transformers

.PHONY: run
run: protogen
	@echo "Running transformers..."
	bash run.sh
	@echo "transformers run."

# It is not working well by using command line. It only6 works with IDE like VSCode.
.PHONY: test
test: protogen
	@echo "Testing transformers..."
	bash test.sh
	@echo "transformers tested."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto