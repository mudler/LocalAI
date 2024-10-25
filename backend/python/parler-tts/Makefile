export CONDA_ENV_PATH = "parler.yml"
SKIP_CONDA?=0
ifeq ($(BUILD_TYPE), cublas)
export CONDA_ENV_PATH = "parler-nvidia.yml"
endif

# Intel GPU are supposed to have dependencies installed in the main python
# environment, so we skip conda installation for SYCL builds.
# https://github.com/intel/intel-extension-for-pytorch/issues/538
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
export SKIP_CONDA=1
endif

.PHONY: parler-tts
parler-tts:
	@echo "Installing $(CONDA_ENV_PATH)..."
	bash install.sh $(CONDA_ENV_PATH)
	$(MAKE) protogen

.PHONY: run
run: protogen
	@echo "Running transformers..."
	bash run.sh
	@echo "transformers run."

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
	bash protogen.sh

.PHONY: clean
clean: protogen-clean
	$(RM) -r venv __pycache__