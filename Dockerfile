FROM python:3.11

WORKDIR /app

# Install python dependencies
COPY requirements.txt requirements.txt

# auto generate it: uv export --no-hashes --format requirements-txt > requirements.txt
RUN pip install -r requirements.txt

# Copy source code
COPY . .

# Run the app
CMD ["python", "-m", "downly"]