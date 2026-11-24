.PHONY: rerankers
rerankers:
	bash install.sh

.PHONY: run
run: rerankers
	@echo "Running rerankers..."
	bash run.sh
	@echo "rerankers run."

# It is not working well by using command line. It only6 works with IDE like VSCode.
.PHONY: test
test: rerankers
	@echo "Testing rerankers..."
	bash test.sh
	@echo "rerankers tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__