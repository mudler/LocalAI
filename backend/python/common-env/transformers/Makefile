CONDA_ENV_PATH = "transformers.yml"

ifeq ($(BUILD_TYPE), cublas)
	CONDA_ENV_PATH = "transformers-nvidia.yml"
endif

ifeq ($(BUILD_TYPE), hipblas)
	CONDA_ENV_PATH = "transformers-rocm.yml"
endif

# Intel GPU are supposed to have dependencies installed in the main python
# environment, so we skip conda installation for SYCL builds.
# https://github.com/intel/intel-extension-for-pytorch/issues/538
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
export SKIP_CONDA=1
endif

.PHONY: transformers
transformers:
	@echo "Installing $(CONDA_ENV_PATH)..."
	bash install.sh $(CONDA_ENV_PATH)
