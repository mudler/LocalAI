.PHONY: mamba
mamba:
	$(MAKE) -C ../common-env/transformers
	bash install.sh

.PHONY: run
run:
	@echo "Running mamba..."
	bash run.sh
	@echo "mamba run."

.PHONY: test
test:
	@echo "Testing mamba..."
	bash test.sh
	@echo "mamba tested."