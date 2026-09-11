.PHONY: funasr
funasr:
	bash install.sh

.PHONY: run
run: funasr
	@echo "Running funasr..."
	bash run.sh
	@echo "funasr run."

.PHONY: test
test: funasr
	@echo "Testing funasr..."
	bash test.sh
	@echo "funasr tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__
