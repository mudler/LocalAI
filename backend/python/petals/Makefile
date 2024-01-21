.PHONY: petals
petals:
	@echo "Creating virtual environment..."
	@conda env create --name petals --file petals.yml
	@echo "Virtual environment created."

.PHONY: run
run:
	@echo "Running petals..."
	bash run.sh
	@echo "petals run."

.PHONY: test
test:
	@echo "Testing petals..."
	bash test.sh
	@echo "petals tested."
