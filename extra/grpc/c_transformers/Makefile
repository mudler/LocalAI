.PONY: ctransformers
ctransformers:
	@echo "Creating virtual environment..."
	@conda create -n ctransformers python=3.11 -y
	@echo "Virtual environment created."

	@echo "Activating virtual environment..."
	@. activate ctransformers

	@echo "Installing dependencies..."
	@pip install grpcio==1.59.0 protobuf==4.24.4

	# Install ctransformers from JLLLLLL's cuBLAS wheels will append cu117to version of ctransformer, this will cause creating from file failed.
	@echo "Installing ctransformers..."
	@pip install ctransformers==0.2.27 --prefer-binary --extra-index-url=https://jllllll.github.io/ctransformers-cuBLAS-wheels/AVX2/cu117

	@echo "Deactivating virtual environment..."
	@. deactivate