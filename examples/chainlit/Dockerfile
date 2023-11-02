# Use an official Python runtime as a parent image
FROM harbor.home.sfxworks.net/docker/library/python:3.9-slim

# Set the working directory in the container
WORKDIR /app

# Copy the current directory contents into the container at /app
COPY requirements.txt /app

# Install any needed packages specified in requirements.txt
RUN pip install -r requirements.txt

COPY . /app

# Run app.py when the container launches
CMD ["chainlit", "run", "-h", "--host", "0.0.0.0", "main.py" ]
