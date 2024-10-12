FROM python:3.13-bullseye
COPY ./langchainpy-localai-example /app
WORKDIR /app
RUN pip install --no-cache-dir -r requirements.txt
ENTRYPOINT [ "python", "./full_demo.py" ]
