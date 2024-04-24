.PHONY: rerankers
rerankers: protogen
	$(MAKE) -C ../common-env/transformers


.PHONY: run
run: protogen
	@echo "Running rerankers..."
	bash run.sh
	@echo "rerankers run."

# It is not working well by using command line. It only6 works with IDE like VSCode.
.PHONY: test
test: protogen
	@echo "Testing rerankers..."
	bash test.sh
	@echo "rerankers tested."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto