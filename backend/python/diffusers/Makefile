export CONDA_ENV_PATH = "diffusers.yml"

ifeq ($(BUILD_TYPE), hipblas)
export CONDA_ENV_PATH = "diffusers-rocm.yml"
endif

# Intel GPU are supposed to have dependencies installed in the main python
# environment, so we skip conda installation for SYCL builds.
# https://github.com/intel/intel-extension-for-pytorch/issues/538
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
export SKIP_CONDA=1
endif

.PHONY: diffusers
diffusers: protogen
	@echo "Installing $(CONDA_ENV_PATH)..."
	bash install.sh $(CONDA_ENV_PATH)

.PHONY: run
run: protogen
	@echo "Running diffusers..."
	bash run.sh
	@echo "Diffusers run."

test: protogen
	bash test.sh

.PHONY: protogen
protogen: backend_pb2_grpc.py backend_pb2.py

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

backend_pb2_grpc.py backend_pb2.py:
	python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto