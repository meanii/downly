FROM python:3.11

WORKDIR /app

# Install ffmpeg
RUN apt update && apt install -y ffmpeg

# Install python dependencies
COPY requirements.txt requirements.txt
RUN pip install -r requirements.txt

# Copy source code
COPY . .

# Run the app
CMD ["python", "-m", "downly"]
