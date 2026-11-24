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
diffusers:
	bash install.sh

.PHONY: run
run: diffusers
	@echo "Running diffusers..."
	bash run.sh
	@echo "Diffusers run."

test: diffusers
	bash test.sh

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__