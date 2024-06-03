.PHONY: sentencetransformers
sentencetransformers: protogen
	bash ./install.sh


.PHONY: run
run: protogen
	@echo "Running sentencetransformers..."
	bash run.sh
	@echo "sentencetransformers run."

# It is not working well by using command line. It only6 works with IDE like VSCode.
.PHONY: test
test: protogen
	@echo "Testing sentencetransformers..."
	bash test.sh
	@echo "sentencetransformers tested."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__