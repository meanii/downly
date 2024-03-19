NAME="downly"

PYTHON_GLOBAL=python3
ENV_NAME=.venv
PYTHON_BIN=${ENV_NAME}/bin/python3

clean:
	@echo "Cleaning cache files..."
	@rm -rf .venv
	@find -iname "*.pyc" -delete
	@echo "Cache files cleaned successfully!"

deps:
	@echo "Installing dependencies..."
	@pip install --upgrade pip
	( \
       source .venv/bin/activate; \
       pip install -r requirements.txt; \
  )
	@echo "Dependencies installed successfully!"

setup: clean
	@echo "Creating virtual environment..."
	@${PYTHON_GLOBAL} -m venv .venv
	@echo "Virtual environment created successfully!"

	# Install dependencies
	@make deps

run:
	# Run the application
	@echo "Running application..."
	@${PYTHON_BIN} -m downly
	@echo "Application running successfully!"

watch:

	@if [ -z $(shell which watchmedo) ]; then \
		echo "Installing watchmedo..."; \
		${PYTHON_BIN} -m pip install watchdog; \
	fi

	@echo "Running application in watch mode..."
	@watchmedo auto-restart --directory=. --pattern=*.py --recursive -- \
		make run

