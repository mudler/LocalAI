.DEFAULT_GOAL := install

.PHONY: install
install:
	bash install.sh
	$(MAKE) protogen

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	bash protogen.sh

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__