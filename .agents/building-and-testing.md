# Build and Testing

Building and testing the project depends on the components involved and the platform where development is taking place. Due to the amount of context required it's usually best not to try building or testing the project unless the user requests it. If you must build the project then inspect the Makefile in the project root and the Makefiles of any backends that are effected by changes you are making. In addition the workflows in .github/workflows can be used as a reference when it is unclear how to build or test a component. The primary Makefile contains targets for building inside or outside Docker, if the user has not previously specified a preference then ask which they would like to use.

## Building a specified backend

Let's say the user wants to build a particular backend for a given platform. For example let's say they want to build coqui for ROCM/hipblas

- The Makefile has targets like `docker-build-coqui` created with `generate-docker-build-target` at the time of writing. Recently added backends may require a new target.
- At a minimum we need to set the BUILD_TYPE, BASE_IMAGE build-args
  - Use .github/workflows/backend.yml as a reference it lists the needed args in the `include` job strategy matrix
  - l4t and cublas also requires the CUDA major and minor version
- You can pretty print a command like `DOCKER_MAKEFLAGS=-j$(nproc --ignore=1) BUILD_TYPE=hipblas BASE_IMAGE=rocm/dev-ubuntu-24.04:7.2.1 make docker-build-coqui`
- Unless the user specifies that they want you to run the command, then just print it because not all agent frontends handle long running jobs well and the output may overflow your context
- The user may say they want to build AMD or ROCM instead of hipblas, or Intel instead of SYCL or NVIDIA insted of l4t or cublas. Ask for confirmation if there is ambiguity.
- Sometimes the user may need extra parameters to be added to `docker build` (e.g. `--platform` for cross-platform builds or `--progress` to view the full logs), in which case you can generate the `docker build` command directly.
