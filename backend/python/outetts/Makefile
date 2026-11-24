.PHONY: outetts
outetts:
	bash install.sh

.PHONY: run
run: outetts
	@echo "Running outetts..."
	bash run.sh
	@echo "outetts run."

.PHONY: test
test: outetts
	@echo "Testing outetts..."
	bash test.sh
	@echo "outetts tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
