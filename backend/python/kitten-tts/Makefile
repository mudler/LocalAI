.PHONY: kitten-tts
kitten-tts: protogen
	bash install.sh

.PHONY: run
run: protogen
	@echo "Running kitten-tts..."
	bash run.sh
	@echo "kitten-tts run."

.PHONY: test
test: protogen
	@echo "Testing kitten-tts..."
	bash test.sh
	@echo "kitten-tts tested."

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. -I./ --python_out=. --grpc_python_out=. backend.proto

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__