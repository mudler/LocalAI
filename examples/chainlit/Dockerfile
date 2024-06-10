# Use an official Python runtime as a parent image
FROM python:3.12-slim

# Set the working directory in the container
WORKDIR /app

# Copy the current directory contents into the container at /app
COPY requirements.txt /app

# Install c++ compiler
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y build-essential \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

# Install any needed packages specified in requirements.txt
RUN pip install --no-cache-dir -r requirements.txt \
 && DEBIAN_FRONTEND=noninteractive apt-get remove -y build-essential \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

COPY . /app

# Run app.py when the container launches
CMD ["chainlit", "run", "-h", "--host", "0.0.0.0", "main.py" ]
