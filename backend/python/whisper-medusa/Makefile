.DEFAULT_GOAL := install

.PHONY: install
install:
	bash install.sh

.PHONY: clean
clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py
	rm -rf venv __pycache__

.PHONY: test
test:
	python3 -m unittest test_unit.py
